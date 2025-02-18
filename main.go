package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"

	green = color.New(color.FgGreen).SprintFunc()
	red   = color.New(color.FgRed).SprintFunc()

	vendor     string
	gcpProject string // for GCP only
	gcpRegion  string // for GCP only
	stdout     bool
	stderr     bool
	idFile     string
	mtx        sync.Mutex
	cs         map[string]*exec.Cmd

	rootCmd = &cobra.Command{
		Use:   "g-ssh-cmd <group-name> 'cmd'",
		Short: "A simple wrapper to [ssh -i key ec2-user@target -t 'cmd'] for AWS AutoScaling Groups",
		Long: `A simple wrapper to [ssh -i key ec2-user@target -t 'cmd'] for AWS AutoScaling Groups.

[version=` + version + `, commit=` + commit + `]`,
		Run:          run,
		SilenceUsage: true,
	}
)

// AWS-related definitions
type instanceT struct {
	// AWS
	InstanceId      string
	PublicIpAddress string

	// GCP
	Instance string `json:"instance"`
}

type asgT struct {
	Instances []instanceT
}

type asgW struct {
	AutoScalingGroups []asgT
}

type reservationT struct {
	Instances []instanceT
}

type instW struct {
	Reservations []reservationT
}

// GCP-related definitions
type migW struct {
	Name   string `json:"name"`
	Region string `json:"region"`
}

func main() {
	go func() {
		s := make(chan os.Signal, 1)
		signal.Notify(s, syscall.SIGINT, syscall.SIGTERM)
		sig := fmt.Errorf("%s", <-s)
		_ = sig

		for _, c := range cs {
			err := c.Process.Signal(syscall.SIGTERM)
			if err != nil {
				info("failed to terminate process, force kill...")
				_ = c.Process.Signal(syscall.SIGKILL)
			}
		}

		os.Exit(0)
	}()

	rootCmd.Flags().StringVar(&vendor, "vendor", "aws", "target vendor, values: 'aws', 'gcp'")
	rootCmd.Flags().StringVar(&idFile, "id-file", "", "identity file, input to -i in ssh")
	rootCmd.Flags().BoolVar(&stdout, "stdout", true, "print stdout output")
	rootCmd.Flags().BoolVar(&stderr, "stderr", true, "print stderr output")
	rootCmd.Flags().StringVar(&gcpProject, "gcp-project", "", "GCP project name, optional (inferred)")
	rootCmd.Flags().StringVar(&gcpRegion, "gcp-region", "", "GCP region, optional (inferred)")
	rootCmd.Execute()
}

func run(cmd *cobra.Command, args []string) {
	if len(args) < 1 {
		fail("no scaling group name provided")
		return
	}

	if len(args) < 2 {
		fail("no command(s) provided")
		return
	}

	cs = make(map[string]*exec.Cmd)

	switch vendor {
	case "aws":
		xcmd := exec.Command(
			"aws",
			"autoscaling",
			"describe-auto-scaling-groups",
			"--auto-scaling-group-name",
			args[0],
		)

		out, err := xcmd.CombinedOutput()
		if err != nil {
			fail(err)
			return
		}

		var t asgW
		err = json.Unmarshal(out, &t)
		if err != nil {
			fail(err)
			return
		}

		for _, i := range t.AutoScalingGroups {
			var wg sync.WaitGroup
			for _, j := range i.Instances {
				wg.Add(1)
				go func(id string) {
					defer wg.Done()
					xcmd = exec.Command(
						"aws",
						"ec2",
						"describe-instances",
						"--instance-ids",
						id,
					)

					iout, err := xcmd.CombinedOutput()
					if err != nil {
						fail(err)
						return
					}

					var v instW
					err = json.Unmarshal(iout, &v)
					if err != nil {
						fail(err)
						return
					}

					for _, x := range v.Reservations {
						for _, y := range x.Instances {
							addcmd := exec.Command(
								"ssh",
								"-i",
								idFile,
								"-o",
								"StrictHostKeyChecking=accept-new",
								fmt.Sprintf("ec2-user@%v", y.PublicIpAddress),
								"-t",
								args[1],
							)

							mtx.Lock()
							cs[id] = addcmd
							mtx.Unlock()
						}
					}
				}(j.InstanceId)
			}

			wg.Wait()
		}
	case "gcp":
		var line strings.Builder
		if gcpProject == "" || gcpRegion == "" {
			fmt.Fprintf(&line, "gcloud compute instance-groups managed list ")
			fmt.Fprintf(&line, "--filter=\"name=('%v')\" --format json", args[0])
			out, err := exec.Command("bash", "-c", line.String()).CombinedOutput()
			if err != nil {
				fail(err)
				return
			}

			var t []migW
			err = json.Unmarshal(out, &t)
			if err != nil {
				fail(err)
				return
			}

			for _, v := range t {
				ss := strings.Split(v.Region, "/")
				// Fmt: https://www.googleapis.com/compute/v1/projects/v/regions/v
				if len(ss) >= 9 {
					gcpProject = ss[6]
					gcpRegion = ss[8]
				}
			}
		}

		line.Reset()
		fmt.Fprintf(&line, "gcloud compute instance-groups managed list-instances rmig ")
		fmt.Fprintf(&line, "--project=%v --region=%v --format json", gcpProject, gcpRegion)
		out, err := exec.Command("bash", "-c", line.String()).CombinedOutput()
		if err != nil {
			fail(err)
			return
		}

		var v []instanceT
		err = json.Unmarshal(out, &v)
		if err != nil {
			fail(err)
			return
		}

		for _, i := range v {
			// Fmt: https://www.googleapis.com/compute/v1/projects/v/zones/v/instances/name
			ss := strings.Split(i.Instance, "/")
			if len(ss) < 11 {
				continue
			}

			name := ss[10]
			zone := ss[8]
			info(name, zone)

			var add strings.Builder
			fmt.Fprintf(&add, "gcloud compute ssh --zone %v %v ", zone, name)
			fmt.Fprintf(&add, "--project %v --quiet --command='%v' -- -t", gcpProject, args[1])
			addcmd := exec.Command("bash", "-c", add.String())

			mtx.Lock()
			cs[name] = addcmd
			mtx.Unlock()
		}
	default:
		fail("invalid vendor")
		return
	}

	if len(cs) == 0 {
		info("no detected targets")
		return
	}

	// Start all cmds.
	var wg sync.WaitGroup
	for k, c := range cs {
		info("connecting to", k, "through", green(c.Args))
		wg.Add(1)
		go func(id string, scmd *exec.Cmd) {
			defer wg.Done()
			outpipe, err := scmd.StdoutPipe()
			if err != nil {
				failx(err)
			}

			errpipe, err := scmd.StderrPipe()
			if err != nil {
				failx(err)
			}

			err = scmd.Start()
			if err != nil {
				failx(err)
			}

			var pwg sync.WaitGroup
			if stdout {
				pwg.Add(1)
				go func() {
					defer pwg.Done()
					outscan := bufio.NewScanner(outpipe)
					for {
						chk := outscan.Scan()
						if !chk {
							break
						}

						stxt := outscan.Text()
						log.Printf("%v|stdout: %v", green(id), stxt)
					}
				}()
			}

			if stderr {
				pwg.Add(1)
				go func() {
					defer pwg.Done()
					errscan := bufio.NewScanner(errpipe)
					for {
						chk := errscan.Scan()
						if !chk {
							break
						}

						stxt := errscan.Text()
						log.Printf("%v|stderr: %v", green(id), stxt)
					}
				}()
			}

			scmd.Wait()
			pwg.Wait()
		}(k, c)
	}

	wg.Wait()
}

func info(v ...interface{}) {
	m := fmt.Sprintln(v...)
	log.Printf("%s %s", green("[info]"), m)
}

func fail(v ...interface{}) {
	m := fmt.Sprintln(v...)
	log.Printf("%s %s", red("[error]"), m)
}

func failx(v ...interface{}) {
	fail(v...)
	os.Exit(1)
}
