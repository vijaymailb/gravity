/*
Copyright 2018 Gravitational, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package report

import (
	"context"
	"fmt"
	"io/ioutil"
	"path/filepath"

	"github.com/gravitational/gravity/lib/defaults"
	"github.com/gravitational/gravity/lib/utils"
	"github.com/gravitational/trace"
)

// NewSystemCollector returns a list of collectors to fetch system information
func NewSystemCollector() Collectors {
	var collectors Collectors
	add := func(additional ...Collector) {
		collectors = append(collectors, additional...)
	}

	add(basicSystemInfo()...)
	add(planetServices()...)
	add(syslogExportLogs())
	add(systemFileLogs()...)
	add(planetLogs()...)

	return collectors
}

func basicSystemInfo() Collectors {
	return Collectors{
		// networking
		Cmd("iptables", "iptables-save"),
		Cmd("route", "route", "-n"),
		Cmd("ifconfig", "ifconfig", "-a"),
		Cmd("ip-addr", "ip", "-d", "addr", "show"),
		Cmd("ip-route", "ip", "-d", "route", "show", "table", "all"),
		Cmd("ip-link", "ip", "-d", "link", "show"),
		Cmd("ip-neighbor", "ip", "-d", "neighbor", "show"),
		Cmd("ip-rule", "ip", "-d", "rule", "show"),
		Cmd("ip-ntable", "ip", "-d", "ntable", "show"),
		Cmd("ip-maddress", "ip", "-d", "maddress", "show"),
		Cmd("ip-xfrm-state", "ip", "-d", "xfrm", "state", "show"),
		Cmd("ip-xfrm-policy", "ip", "-d", "xfrm", "policy", "show"),
		Cmd("ip-tcp_metrics", "ip", "-d", "tcp_metrics", "show"),
		Cmd("ip-netconf", "ip", "-d", "netconf", "show"),
		Cmd("bridge-fdb", utils.PlanetCommandArgs("/sbin/bridge", "fdb", "show")...),
		// disk
		Cmd("lsblk", "lsblk"),
		Cmd("fdisk", "fdisk", "-l"),
		Cmd("df", "df", "-hT"),
		Cmd("df-inodes", "df", "-i"),
		// system
		Cmd("lscpu", "lscpu"),
		Cmd("lsmod", "lsmod"),
		Cmd("running-processes", "/bin/ps", "auxZ", "--forest"),
		Cmd("host-system-status", "/bin/systemctl", "status", "--full"),
		Cmd("host-system-failed", "/bin/systemctl", "--failed", "--full"),
		Cmd("host-system-jobs", "/bin/systemctl", "list-jobs", "--full"),
		Cmd("dmesg", "/bin/dmesg", "--raw"),
		// Fetch world-readable parts of /etc/
		fetchEtc("etc-logs.tar.gz"),
		// memory
		Cmd("free", "free", "--human"),
		Cmd("slabtop", "slabtop", "--once"),
		Cmd("vmstat", "vmstat", "--stats"),
		Cmd("slabinfo", "cat", "/proc/slabinfo"),
	}
}

func planetServices() Collectors {
	return Collectors{
		// etcd cluster health
		Cmd("etcd-status", utils.PlanetCommandArgs("/usr/bin/etcdctl", "cluster-health")...),
		Cmd("etcd3-status", utils.PlanetCommandArgs("/usr/bin/etcdctl3", "endpoint", "health", "--cluster")...),
		Cmd("planet-status", utils.PlanetCommandArgs("/usr/bin/planet", "status")...),
		// system status in the container
		Cmd("planet-system-status", utils.PlanetCommandArgs("/bin/systemctl", "status", "--full")...),
		Cmd("planet-system-failed", utils.PlanetCommandArgs("/bin/systemctl", "--failed", "--full")...),
		Cmd("planet-system-jobs", utils.PlanetCommandArgs("/bin/systemctl", "list-jobs", "--full")...),
	}
}

// syslogExportLogs fetches host journal logs
func syslogExportLogs() Collector {
	const script = `
#!/bin/bash
/bin/journalctl --no-pager --output=export | /bin/gzip -f`
	return Script("gravity-system.log.gz", script)
}

// systemFileLogs fetches gravity platform-related logs
func systemFileLogs() Collectors {
	const template = `
#!/bin/bash
cat %v 2> /dev/null || true`
	workingDir := filepath.Dir(utils.Exe.Path)
	return Collectors{
		Script("gravity-system.log", fmt.Sprintf(template, defaults.GravitySystemLogPath)),
		Script("gravity-system-local.log", fmt.Sprintf(template, filepath.Join(workingDir, defaults.GravitySystemLogFile))),
		Script("gravity-install.log", fmt.Sprintf(template, defaults.GravityUserLogPath)),
		Script("gravity-install-local.log", fmt.Sprintf(template, filepath.Join(workingDir, defaults.GravityUserLogFile))),
	}
}

// planetLogs fetches planet syslog messages as well as the fresh journal entries
func planetLogs() Collectors {
	return Collectors{
		// Fetch planet journal entries for the last two days
		// The log can be imported as a journal with systemd-journal-remote:
		//
		// $ cat ./node-1-planet-journal-export.log | /lib/systemd/systemd-journal-remote -o ./journal/system.journal -
		Self("planet-journal-export.log.gz",
			"system", "export-runtime-journal"),
	}
}

// etcdBackup fetches etcd data for gravity and planet
func etcdBackup() Collectors {
	return Collectors{
		Cmd("etcd-backup.json", utils.PlanetCommandArgs(defaults.PlanetBin, "etcd", "backup",
			"--prefix", defaults.EtcdPlanetPrefix,
			"--prefix", defaults.EtcdGravityPrefix)...),
	}
}

func fetchEtc(name string) CollectorFunc {
	args := []string{
		"cz", "--ignore-failed-read", "--dereference", "--ignore-command-error",
		"--absolute-names", "--directory", "/",
		"--exclude=/etc/ssl/**", "--exclude=/etc/fonts/**",
		"/etc",
	}
	return CollectorFunc(func(ctx context.Context, reportWriter FileWriter, _ utils.CommandRunner) error {
		w, err := reportWriter.NewWriter(name)
		if err != nil {
			return trace.Wrap(err)
		}
		return utils.ExecUnprivileged(ctx, "/bin/tar", args,
			utils.Stderr(ioutil.Discard),
			utils.Stdout(w),
		)
	})
}
