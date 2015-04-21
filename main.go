package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"os/user"
	"regexp"
	"sort"
	"strings"

	"github.com/Sirupsen/logrus"
	"github.com/jessevdk/go-flags"

	"gopkg.in/yaml.v2"
)

type hostConf struct {
	Via            string `yaml:"Via"`
	Hostname       string `yaml:"Hostname"`
	GatewayCommand string `yaml:"GatewayCommand"`
}

type conf struct {
	Hosts    map[string]hostConf `yaml:"hosts"`
	Includes []string            `yaml:"include"`
}

func expandHomeDir(path string) string {
	usr, _ := user.Current()
	dir := usr.HomeDir + "/"
	if path[:2] == "~/" {
		path = strings.Replace(path, "~/", dir, 1)
	}
	return path
}
func loadConf(path string) (*conf, error) {
	b, err := ioutil.ReadFile(expandHomeDir(path))
	if err != nil {
		return nil, err
	}
	v := &conf{Hosts: make(map[string]hostConf)}
	err = yaml.Unmarshal(b, v)

	for _, p := range v.Includes {
		inc, err := loadConf(p)
		if err != nil {
			return nil, err
		}
		v.merge(inc)
	}
	return v, err
}

func (c *conf) merge(other *conf) {
	for k, v := range other.Hosts {
		if _, ok := c.Hosts[k]; !ok {
			c.Hosts[k] = v
		} else {
			logrus.Infof("%s is ignored", k)
		}
	}
}

func tmux(cmds []string) {
	var c *exec.Cmd
	c = exec.Command("tmux", "new-window", strings.Join(cmds, " "))
	err := c.Start()
	if err != nil {
		logrus.Errorf("failed to start tmux: %s", err)
	}
}

func ssh(cmds []string) {
	c := exec.Command(cmds[0], cmds[1:]...)
	c.Stdin = os.Stdin
	sout, _ := c.StdoutPipe()
	serr, _ := c.StderrPipe()
	go io.Copy(os.Stdout, sout)
	go io.Copy(os.Stderr, serr)
	if err := c.Start(); err != nil {
		logrus.Errorf("failed to start ssh: %s", err)
	}
	if err := c.Wait(); err != nil {
		logrus.Errorf("failed to wait ssh: %s", err)
	}
}

func getSSHCmdline(name string, c *conf) []string {
	h := c.Hosts[name]
	cmds := []string{}
	if h.Hostname != "" {
		name = h.Hostname
	}

	ssh := []string{"ssh", "-t"}
	if h.GatewayCommand != "" {
		ssh = strings.Split(h.GatewayCommand, " ")
	}

	if h.Via == "" {
		cmds = append(cmds, ssh...)
		cmds = append(cmds, name)
	} else {
		cmds = append(cmds, []string{"ssh", "-t"}...)
		cmds = append(cmds, h.Via)
		cmds = append(cmds, fmt.Sprintf("%s %s", h.GatewayCommand, name))
	}
	logrus.Info(cmds)
	return cmds
}

var opts struct {
	ConfigFile  string `short:"c" long:"config-file"  description:"Config file path"           default:"~/.mssh"`
	Dryrun      bool   `short:"n" long:"dry-run"      description:"Show target hosts and exit" default:"false"`
	FixedString bool   `short:"f" long:"fixed-string" description:"Fixed string"               default:"false"`
	Yes         bool   `short:"y" long:"yes"          description:"Say yes"                    default:"false"`
	UseTMUX     bool   `short:"t" long:"tmux"         description:"Use tmux"                   default:"false"`
	Positional  struct {
		Self    string
		Filters []string
	} `positional-args:"yes" required:"yes"`
}

func askyn() bool {
	b := make([]byte, 1)
	_, err := os.Stdin.Read(b)
	if err != nil {
		logrus.Errorf("Error in askyn: %s", err)
		return false
	}
	switch b[0] {
	case 'y', 'Y':
		return true
	}
	return false
}

func filter(elems []string, f string, re bool) []string {
	r := regexp.MustCompile(f)
	matched := []string{}
	for _, k := range elems {
		if strings.HasPrefix(k, "_") {
			continue
		}
		if !re && f == k {
			matched = append(matched, k)
		} else if re && r.MatchString(k) {
			matched = append(matched, k)
		}
	}
	return matched
}

func getTargets(hosts, filters []string, op string, re bool) []string {
	switch op {
	case "AND", "OR":
	default:
		return nil
	}
	targets := []string{}
	all := hosts

	for _, f := range filters {
		matched := filter(all, f, re)
		switch op {
		case "AND":
			all = matched
		case "OR":
			targets = append(targets, matched...)
			logrus.Info(targets)
		}
	}
	if op == "AND" {
		targets = all
	}
	return targets
}

func main() {
	_, err := flags.ParseArgs(&opts, os.Args)
	if err != nil {
		logrus.Fatal(err)
	}

	c, err := loadConf(opts.ConfigFile)
	if err != nil {
		logrus.Fatal(err)
	}

	re := true
	op := "AND"
	if opts.FixedString {
		re = false
		op = "OR"
	}
	cands := make([]string, len(c.Hosts))
	for k := range c.Hosts {
		cands = append(cands, k)
	}

	hosts := getTargets(cands, opts.Positional.Filters, op, re)
	sort.Strings(hosts)

	for _, v := range hosts {
		fmt.Printf("%s\n", v)
	}

	if opts.Dryrun {
		return
	}

	if !opts.UseTMUX && len(hosts) > 1 {
		logrus.Fatal("Can't open multipe hosts. please use tmux mode.")
	}
	if len(hosts) > 2 && !opts.Yes {
		fmt.Printf("Too many hosts selected\nInput `y` to continue: ")
		if !askyn() {
			logrus.Fatal("Interrupted")
		}
	}

	for _, host := range hosts {
		cmds := getSSHCmdline(host, c)
		if opts.UseTMUX {
			tmux(cmds)
		} else {
			ssh(cmds)
		}
	}
}
