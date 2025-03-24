package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
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

	profile string // for AWS only
	project string // for GCP only
	only    string // GCP only for now
	stdout  bool
	stderr  bool
	key     string // SSH key for AWS ASG
	mtx     sync.Mutex
	cs      map[string]*exec.Cmd

	rootCmd = &cobra.Command{
		Use:   "g-ssh-cmd <asg|mig> <group-name> <cmd>",
		Short: "A simple wrapper to [ssh user@host -t 'cmd'] for AWS ASGs and GCP MIGs",
		Long: `A simple wrapper to [ssh user@host -t 'cmd'] for AWS ASGs and GCP MIGs.

[version=` + version + `, commit=` + commit + `]`,
		Run:          run,
		SilenceUsage: true,
	}
)

// AWS and GCP combined, we only define what we need.
type instanceT struct {
	// AWS
	InstanceId      string
	PublicIpAddress string

	// GCP
	Instance string `json:"instance"`
}

// AWS-related defintions.
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

// GCP-related definitions.
type migW struct {
	Name   string `json:"name"`
	Region string `json:"region"`
	Zone   string `json:"zone"`
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

	log.SetOutput(os.Stdout) // for easy grep
	rootCmd.Flags().SortFlags = false
	rootCmd.Flags().StringVar(&key, "key", "", "identity file, input to -i in ssh (AWS only)")
	rootCmd.Flags().BoolVar(&stdout, "stdout", true, "print stdout output")
	rootCmd.Flags().BoolVar(&stderr, "stderr", true, "print stderr output")
	rootCmd.Flags().StringVar(&profile, "profile", "", "AWS profile, valid only if 'asg', optional")
	rootCmd.Flags().StringVar(&project, "project", "", "GCP project, valid only if 'mig', optional")
	rootCmd.Flags().StringVar(&only, "only", "", "filter: only these instances (supports glob/wildcards), valid only if 'mig', optional")
	rootCmd.Execute()
}

func run(cmd *cobra.Command, args []string) {
	if len(args) < 3 {
		fail("invalid arguments, see -h")
		return
	}

	cs = make(map[string]*exec.Cmd)

	switch args[0] {
	case "asg":
		line := []string{
			"autoscaling",
			"describe-auto-scaling-groups",
			"--auto-scaling-group-name",
			args[1],
		}

		if profile != "" {
			line = append(line, "--profile", profile)
		}

		out, err := exec.Command("aws", line...).CombinedOutput()
		if err != nil {
			fail(err, "-->", string(out))
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
					line := []string{
						"ec2",
						"describe-instances",
						"--instance-ids",
						id,
					}

					if profile != "" {
						line = append(line, "--profile", profile)
					}

					b, err := exec.Command("aws", line...).CombinedOutput()
					if err != nil {
						fail(err, "-->", string(b))
						return
					}

					var v instW
					err = json.Unmarshal(b, &v)
					if err != nil {
						fail(err)
						return
					}

					for _, x := range v.Reservations {
						for _, y := range x.Instances {
							addcmd := exec.Command(
								"ssh",
								"-i",
								key,
								"-o",
								"StrictHostKeyChecking=accept-new",
								fmt.Sprintf("ec2-user@%v", y.PublicIpAddress),
								"-t",
								args[2],
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
	case "mig":
		var line strings.Builder
		fmt.Fprintf(&line, "gcloud compute instance-groups managed list --format=json")
		if project != "" {
			fmt.Fprintf(&line, " --project=%v", project)
		}

		out, err := exec.Command("sh", "-c", line.String()).CombinedOutput()
		if err != nil {
			fail(err, "-->", string(out))
			return
		}

		var t []migW
		err = json.Unmarshal(out, &t)
		if err != nil {
			fail(err)
			return
		}

		var region, zone string
		var found bool
		for _, v := range t {
			if v.Name != args[1] {
				continue
			}

			found = true
			if v.Region != "" && region == "" {
				// Fmt: https://www.googleapis.com/compute/v1/projects/v/regions/v
				ss := strings.Split(v.Region, "/")
				if len(ss) >= 9 {
					region = ss[8]
				}
			}

			if v.Zone != "" && zone == "" {
				// Fmt: https://www.googleapis.com/compute/v1/projects/v/zones/v
				ss := strings.Split(v.Zone, "/")
				if len(ss) >= 9 {
					zone = ss[8]
				}
			}
		}

		if !found {
			fail(args[1], "not found")
			return
		}

		line.Reset()
		fmt.Fprintf(&line, "gcloud compute instance-groups managed ")
		fmt.Fprintf(&line, "list-instances %v --format=json", args[1])
		if project != "" {
			fmt.Fprintf(&line, " --project=%v", project)
		}

		if region != "" {
			fmt.Fprintf(&line, " --region=%v", region)
		}

		if zone != "" {
			fmt.Fprintf(&line, " --zone=%v", zone)
		}

		out, err = exec.Command("sh", "-c", line.String()).CombinedOutput()
		if err != nil {
			fail(err, "-->", string(out))
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
			sshZone := ss[8]

			if only != "" {
				matched, err := matchPattern(name, only)
				if err != nil {
					fail(err)
					continue
				}
				if !matched {
					continue // Skip this VM if it doesn't match the filter
				}
			}

			var add strings.Builder
			fmt.Fprintf(&add, "gcloud compute ssh --zone %v %v --quiet", sshZone, name)
			if project != "" {
				fmt.Fprintf(&add, " --project=%v", project)
			}

			fmt.Fprintf(&add, " --command='%v' -- -t", args[2])
			cs[name] = exec.Command("sh", "-c", add.String())
		}
	default:
		fail("invalid argument(s), see -h")
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
						log.Printf("%v|%v: %v", green(id), green("stdout"), stxt)
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
						log.Printf("%v|%v: %v", green(id), red("stderr"), stxt)
					}
				}()
			}

			scmd.Wait()
			pwg.Wait()
		}(k, c)
	}

	wg.Wait()
}

func matchPattern(name, pattern string) (bool, error) {
	// Simple exact match
	if pattern == name {
		return true, nil
	}

	// Use filepath.Match for glob pattern matching
	// This supports * and ? wildcards
	matched, err := filepath.Match(pattern, name)
	if err != nil {
		return false, fmt.Errorf("invalid pattern: %v", err)
	}

	// Also check for contains match if no wildcards
	if !strings.Contains(pattern, "*") && !strings.Contains(pattern, "?") {
		if strings.Contains(name, pattern) {
			return true, nil
		}
	}

	return matched, nil
}

func info(v ...any) {
	m := fmt.Sprintln(v...)
	log.Printf("%s %s", green("[info]"), m)
}

func fail(v ...any) {
	m := fmt.Sprintln(v...)
	log.Printf("%s %s", red("[error]"), m)
}

func failx(v ...any) {
	fail(v...)
	os.Exit(1)
}
