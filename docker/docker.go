package docker

import (
	"bufio"
	"encoding/json"
	"fmt"
	"github.com/ekara-platform/engine/action"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/context"

	"docker.io/go-docker"
	"docker.io/go-docker/api/types"
	"docker.io/go-docker/api/types/container"
	"docker.io/go-docker/api/types/mount"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/tlsconfig"

	"github.com/ekara-platform/cli/common"
	"github.com/ekara-platform/engine/util"
)

// The docker client used within the whole application
var client *docker.Client

//EnsureDockerInit ensures that the Docker client is properly initialized
func EnsureDockerInit() {
	if client == nil {
		var err error
		var c *docker.Client
		if common.Flags.Docker.Cert != "" {
			options := tlsconfig.Options{
				CAFile:             filepath.Join(common.Flags.Docker.Cert, "ca.pem"),
				CertFile:           filepath.Join(common.Flags.Docker.Cert, "cert.pem"),
				KeyFile:            filepath.Join(common.Flags.Docker.Cert, "key.pem"),
				InsecureSkipVerify: common.Flags.Docker.TLS,
			}
			tlsc, err := tlsconfig.Client(options)
			if err != nil {
				panic(err)
			}
			httpClient := &http.Client{
				Transport: &http.Transport{
					Proxy: http.ProxyFromEnvironment,
					DialContext: (&net.Dialer{
						Timeout:   30 * time.Second,
						KeepAlive: 30 * time.Second,
					}).DialContext,
					// ForceAttemptHTTP2:     true, TODO: uncomment with Go 1.13
					MaxIdleConns:          100,
					IdleConnTimeout:       90 * time.Second,
					TLSHandshakeTimeout:   10 * time.Second,
					ExpectContinueTimeout: 1 * time.Second,
					TLSClientConfig:       tlsc,
				},
				CheckRedirect: docker.CheckRedirect,
			}
			c, err = docker.NewClient(common.Flags.Docker.Host, "", httpClient, nil)
		} else {
			c, err = docker.NewClient(common.Flags.Docker.Host, "", nil, nil)
		}

		if err != nil {
			panic(err)
		}
		client = c
	}
}

// ContainerRunningByImageName returns true if a container, built
// on the given image, is running
func ContainerRunningByImageName(name string) (string, bool, error) {
	containers, err := getContainers()
	if err != nil {
		return "", false, nil
	}
	for _, c := range containers {
		if c.Image == name || c.Image+":latest" == name {
			return c.ID, true, nil
		}
	}
	return "", false, nil
}

//containerRunningById returns true if a container with the given id is running
func containerRunningById(id string) (bool, error) {
	containers, err := getContainers()
	if err != nil {
		return false, err
	}
	for _, c := range containers {
		if c.ID == id {
			return true, nil
		}
	}
	return false, nil
}

//stopContainerById stops a container corresponding to the provider id
func StopContainerById(id string, done chan bool) error {
	if err := client.ContainerStop(context.Background(), id, nil); err != nil {
		return err
	}
	if err := client.ContainerRemove(context.Background(), id, types.ContainerRemoveOptions{}); err != nil {
		return err
	}
	for {
		common.Logger.Printf(common.LOG_WAITING_STOP)
		time.Sleep(500 * time.Millisecond)
		stillRunning, err := containerRunningById(id)
		if err != nil {
			return err
		}
		if !stillRunning {
			common.Logger.Printf(common.LOG_STOPPED)
			done <- true
			return nil
		}
	}
}

// StartContainer builds or updates a container base on the provided image name
// Once built the container will be started.
// The method will wait until the container is started and
// will notify it using the chanel
func StartContainer(url string, imageName string, done chan bool, ef util.ExchangeFolder, a action.ActionID) (int, error) {
	envVar := []string{}
	envVar = append(envVar, util.StarterEnvVariableKey+"="+url)
	envVar = append(envVar, util.StarterEnvNameVariableKey+"="+common.Flags.Descriptor.File)
	envVar = append(envVar, util.StarterEnvLoginVariableKey+"="+common.Flags.Descriptor.Login)
	envVar = append(envVar, util.StarterEnvPasswordVariableKey+"="+common.Flags.Descriptor.Password)
	envVar = append(envVar, util.StarterVerbosityVariableKey+"="+strconv.Itoa(common.Flags.Logging.VerbosityLevel()))
	envVar = append(envVar, util.ActionEnvVariableSkip+"="+strconv.Itoa(common.Flags.Skipping.SkippingLevel()))
	envVar = append(envVar, util.ActionEnvVariableKey+"="+a.String())
	envVar = append(envVar, "http_proxy="+common.Flags.Proxy.HTTP)
	envVar = append(envVar, "https_proxy="+common.Flags.Proxy.HTTPS)
	envVar = append(envVar, "no_proxy="+common.Flags.Proxy.Exclusions)

	common.Logger.Printf(common.LOG_PASSING_CONTAINER_ENVARS, envVar)

	// Check if we need to load parameters from the comand line
	if common.Flags.Descriptor.ParamFile != "" {
		copyExtraParameters(common.Flags.Descriptor.ParamFile, ef)
	}

	startedAt := time.Now().UTC()
	startedAt = startedAt.Add(time.Second * -2)
	resp, err := client.ContainerCreate(context.Background(), &container.Config{
		Image:      imageName,
		WorkingDir: util.InstallerVolume,
		Env:        envVar,
	}, &container.HostConfig{
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeBind,
				Source: ef.Location.AdaptedPath(),
				Target: util.InstallerVolume,
			},
			{
				Type:   mount.TypeBind,
				Source: "/var/run/docker.sock",
				Target: "/var/run/docker.sock",
			},
		},
	}, nil, "")

	if err != nil {
		return 0, err
	}

	// Chan used to turn off the rolling log
	stopLogReading := make(chan bool)

	// Rolling output of the container logs
	go func(start time.Time, exit chan bool) {
		logMap := make(map[string]string)
		// Trick to avoid tracing twice the same log line
		notExist := func(s string) (bool, string) {
			tab := strings.Split(s, util.InstallerLogPrefix)
			if len(tab) > 1 {
				sTrim := strings.Trim(tab[1], " ")
				if _, ok := logMap[sTrim]; ok {
					return false, ""
				}
				logMap[sTrim] = ""
				return true, util.InstallerLogPrefix + sTrim
			} else {
				return true, s
			}
		}

		// Request to get the logs content from the container
		req := func(sr string) {
			out, err := client.ContainerLogs(context.Background(), resp.ID, types.ContainerLogsOptions{Since: sr, ShowStdout: true, ShowStderr: true})
			if err != nil {
				stopLogReading <- true
			}
			s := bufio.NewScanner(out)
			for s.Scan() {
				str := s.Text()
				if b, sTrim := notExist(str); b {
					idx := strings.Index(sTrim, util.FeedbackPrefix)
					if idx != -1 {
						fU := util.FeedbackUpdate{}
						err = json.Unmarshal([]byte(sTrim[idx+len(util.FeedbackPrefix):]), &fU)
						if err != nil {
							common.Logger.Println("Unable to parse progress update: " + err.Error())
						} else if !common.Flags.Logging.ShouldOutputLogs() {
							switch fU.Type {
							case "I":
								common.CliFeedbackNotifier.Info(fU.Message)
								break
							case "E":
								common.CliFeedbackNotifier.Error(fU.Message)
								break
							case "P":
								common.CliFeedbackNotifier.ProgressG(fU.Key, fU.Goal, fU.Message)
								break
							case "D":
								common.CliFeedbackNotifier.Detail(fU.Message)
								break
							}
						}
					} else if common.Flags.Logging.ShouldOutputLogs() {
						fmt.Println(sTrim)
					}
				}
			}
			err = out.Close()
			if err != nil {
				common.Logger.Println("Unable to close container log reader: " + err.Error())
			}
		}
	Loop:
		for {
			select {
			case <-exit:
				// Last call to be sure to get the end of the logs content
				now := time.Now()
				now = now.Add(time.Second * -1)
				sinceReq := strconv.FormatInt(now.Unix(), 10)
				req(sinceReq)
				break Loop
			default:
				// Running call to trace the container logs every 500ms
				sinceReq := strconv.FormatInt(start.Unix(), 10)
				start = start.Add(time.Millisecond * 500)
				req(sinceReq)
				time.Sleep(time.Millisecond * 500)
			}
		}
	}(startedAt, stopLogReading)

	defer func() {
		if err := LogAllFromContainer(resp.ID, ef, done); err != nil {
			common.Logger.Println("Unable to fetch logs from container")
		}
	}()

	if err := client.ContainerStart(context.Background(), resp.ID, types.ContainerStartOptions{}); err != nil {
		common.CliFeedbackNotifier.Error("Unable to start container: %s", err.Error())
		return 0, err
	}

	statusCh, errCh := client.ContainerWait(context.Background(), resp.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		stopLogReading <- true
		return 0, err
	case status := <-statusCh:
		stopLogReading <- true
		return int(status.StatusCode), nil
	}
}

func LogAllFromContainer(id string, ef util.ExchangeFolder, done chan bool) error {
	out, err := client.ContainerLogs(context.Background(), id, types.ContainerLogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		// we stop now (cannot fetch any more log)
		done <- true
		return err
	}

	logFile, err := containerLog(ef)
	if err != nil {
		// we stop now (cannot fetch any more log)
		done <- true
		return err
	}
	defer logFile.Close()

	_, err = stdcopy.StdCopy(logFile, logFile, out)
	if err != nil {
		// we stop now (cannot fetch any more log)
		done <- true
		return err
	}

	// We are done!
	common.Logger.Printf(common.LOG_CONTAINER_LOG_WRITTEN, logFile.Name())
	done <- true
	return nil
}

// getContainers returns the detail of all running containers
func getContainers() ([]types.Container, error) {
	containers, err := client.ContainerList(context.Background(), types.ContainerListOptions{})
	if err != nil {
		return []types.Container{}, err
	}
	return containers, nil
}

// imageExistsByName returns true if an image corresponding
// to the given name has been already downloaded
func imageExistsByName(name string) (bool, error) {
	images, err := getImages()
	if err != nil {
		return false, err
	}
	for _, image := range images {
		for _, tag := range image.RepoTags {
			if tag == name {
				return true, nil
			}
		}
	}
	return false, nil
}

// getImages returns the summary of all images already downloaded
func getImages() ([]types.ImageSummary, error) {
	images, err := client.ImageList(context.Background(), types.ImageListOptions{})
	if err != nil {
		return []types.ImageSummary{}, err
	}
	return images, nil
}

// ImagePull pulls the image corresponding to th given name
// and wait for the download to be completed.
//
// The completion of the download will be notified using the chanel
func ImagePull(taggedName string, done chan bool, failed chan error) {
	img, err := imageExistsByName(taggedName)
	if err != nil {
		failed <- err
		return
	}
	if !img {
		if r, err := client.ImagePull(context.Background(), taggedName, types.ImagePullOptions{}); err != nil {
			failed <- err
			return
		} else {
			defer r.Close()
		}
		common.CliFeedbackNotifier.Progress("cli.docker.download", "Downloading installer image")
		for {
			common.Logger.Printf(common.LOG_WAITING_DOWNLOAD)
			time.Sleep(1000 * time.Millisecond)
			img, err := imageExistsByName(taggedName)
			if err != nil {
				failed <- err
				return
			}
			if img {
				common.Logger.Printf(common.LOG_DOWNLOAD_COMPLETED)
				break
			}
		}
	}
	done <- true
}

func copyExtraParameters(file string, ef util.ExchangeFolder) error {
	if _, err := os.Stat(file); err != nil {
		if os.IsNotExist(err) {
			common.Logger.Fatalf(common.ERROR_UNREACHABLE_PARAM_FILE, file)
		}
	}

	b, err := ioutil.ReadFile(file)
	if err != nil {
		return err
	}

	err = ef.Location.Write(b, util.ExternalVarsFilename)
	if err != nil {
		return err
	}

	return nil
}

func containerLog(ef util.ExchangeFolder) (*os.File, error) {
	f, e := os.Create(filepath.Join(ef.Output.Path(), common.Flags.Logging.File))
	if e != nil {
		return nil, e
	}
	return f, nil
}
