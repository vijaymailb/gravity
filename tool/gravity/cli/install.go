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

package cli

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gravitational/gravity/lib/constants"
	"github.com/gravitational/gravity/lib/defaults"
	"github.com/gravitational/gravity/lib/expand"
	"github.com/gravitational/gravity/lib/httplib"
	"github.com/gravitational/gravity/lib/install"
	installerclient "github.com/gravitational/gravity/lib/install/client"
	clinstall "github.com/gravitational/gravity/lib/install/engine/cli"
	"github.com/gravitational/gravity/lib/install/engine/interactive"
	installpb "github.com/gravitational/gravity/lib/install/proto"
	"github.com/gravitational/gravity/lib/localenv"
	"github.com/gravitational/gravity/lib/ops"
	"github.com/gravitational/gravity/lib/ops/resources"
	"github.com/gravitational/gravity/lib/ops/resources/gravity"
	"github.com/gravitational/gravity/lib/process"
	pb "github.com/gravitational/gravity/lib/rpc/proto"
	rpcserver "github.com/gravitational/gravity/lib/rpc/server"
	"github.com/gravitational/gravity/lib/state"
	"github.com/gravitational/gravity/lib/system/environ"
	"github.com/gravitational/gravity/lib/system/service"
	"github.com/gravitational/gravity/lib/system/signals"
	"github.com/gravitational/gravity/lib/systemservice"
	"github.com/gravitational/gravity/lib/utils"
	"github.com/gravitational/gravity/lib/utils/cli"

	"github.com/fatih/color"
	"github.com/gravitational/trace"
)

func startInstall(env *localenv.LocalEnvironment, config InstallConfig) error {
	env.PrintStep("Starting installer")

	if err := config.CheckAndSetDefaults(); err != nil {
		return trace.Wrap(err)
	}
	if config.FromService {
		err := startInstallFromService(env, config)
		if utils.IsContextCancelledError(err) {
			return trace.Wrap(err, "installer interrupted")
		}
		return trace.Wrap(err)
	}
	if err := config.RunLocalChecks(); err != nil {
		return trace.Wrap(err)
	}
	strategy, err := NewInstallerConnectStrategy(env, config, cli.CommandArgs{
		Parser: cli.ArgsParserFunc(parseArgs),
	})
	if err != nil {
		return trace.Wrap(err)
	}
	err = InstallerClient(env, installerclient.Config{
		ConnectStrategy: strategy,
		Lifecycle: &installerclient.AutomaticLifecycle{
			Aborter:            AborterForMode(config.Mode, env),
			Completer:          InstallerCompleteOperation(env),
			DebugReportPath:    DebugReportPath(),
			LocalDebugReporter: InstallerGenerateLocalReport(env),
		},
	})
	if utils.IsContextCancelledError(err) {
		// We only end up here if the initialization has not been successful - clean up the state
		if err := InstallerCleanup(); err != nil {
			log.Warnf("Failed to clean up installer: %v.", err)
		}
		return trace.Wrap(err, "installer interrupted")
	}
	return trace.Wrap(err)
}

func startInstallFromService(env *localenv.LocalEnvironment, config InstallConfig) error {
	ctx, cancel := context.WithCancel(context.Background())
	interrupt := signals.NewInterruptHandler(ctx, cancel, InterruptSignals)
	defer interrupt.Close()
	go TerminationHandler(interrupt, env)
	listener, err := NewServiceListener()
	if err != nil {
		return trace.Wrap(utils.NewPreconditionFailedError(err))
	}
	defer func() {
		if err != nil {
			listener.Close()
		}
	}()
	installerConfig, err := newInstallerConfig(ctx, env, config)
	if err != nil {
		return trace.Wrap(utils.NewPreconditionFailedError(err))
	}
	var installer *install.Installer
	switch config.Mode {
	case constants.InstallModeCLI:
		installer, err = newCLInstaller(ctx, installerConfig)
	case constants.InstallModeInteractive:
		installer, err = newWizardInstaller(ctx, installerConfig)
	default:
		return trace.BadParameter("unknown mode %q", config.Mode)
	}
	if err != nil {
		return trace.Wrap(utils.NewPreconditionFailedError(err))
	}
	interrupt.AddStopper(installer)
	return trace.Wrap(installer.Run(listener))
}

func newInstallerConfig(ctx context.Context, env *localenv.LocalEnvironment, config InstallConfig) (*install.Config, error) {
	processConfig, err := config.NewProcessConfig()
	if err != nil {
		return nil, trace.Wrap(err)
	}
	process, err := install.InitProcess(ctx, *processConfig, process.NewProcess)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	wizard, err := localenv.LoginWizard(processConfig.WizardAddr(), config.Token)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	installerConfig, err := config.NewInstallerConfig(env, wizard, process, resources.ValidateFunc(gravity.Validate))
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return installerConfig, nil
}

func newCLInstaller(ctx context.Context, config *install.Config) (*install.Installer, error) {
	engine, err := clinstall.New(clinstall.Config{
		FieldLogger: config.WithField("mode", "cli"),
		Operator:    config.Operator,
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}
	enablePreflightChecks := true
	installer, err := install.New(ctx, install.RuntimeConfig{
		Config:         *config,
		Planner:        install.NewPlanner(enablePreflightChecks, config),
		FSMFactory:     install.NewFSMFactory(*config),
		ClusterFactory: install.NewClusterFactory(*config),
		Engine:         engine,
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return installer, nil
}

func newWizardInstaller(ctx context.Context, config *install.Config) (*install.Installer, error) {
	disablePreflightChecks := false
	engine, err := interactive.New(interactive.Config{
		FieldLogger:   config.WithField("mode", "wizard"),
		AdvertiseAddr: config.GetWizardAddr(),
		Operator:      config.Operator,
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}
	installer, err := install.New(ctx, install.RuntimeConfig{
		Config:         *config,
		Planner:        install.NewPlanner(disablePreflightChecks, config),
		FSMFactory:     install.NewFSMFactory(*config),
		ClusterFactory: install.NewClusterFactory(*config),
		Engine:         engine,
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return installer, nil
}

// joinClient runs the client for the agent service.
// The client is responsible for starting the RPC agent and observing
// operation progress
func joinClient(env *localenv.LocalEnvironment, config installerclient.Config) error {
	printJoinInstructionsBanner(env)
	return trace.Wrap(installerClient(env, config, "Connecting to agent", "Connected to agent"))
}

func joinFromService(env, joinEnv *localenv.LocalEnvironment, config JoinConfig) error {
	err := config.CheckAndSetDefaults()
	if err != nil {
		return trace.Wrap(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	interrupt := signals.NewInterruptHandler(ctx, cancel, InterruptSignals)
	defer interrupt.Close()
	go TerminationHandler(interrupt, env)
	listener, err := NewServiceListener()
	if err != nil {
		return trace.Wrap(err)
	}
	defer func() {
		if err != nil {
			listener.Close()
		}
	}()
	peerConfig, err := config.NewPeerConfig(env, joinEnv)
	if err != nil {
		return trace.Wrap(err)
	}
	peer, err := expand.NewPeer(*peerConfig)
	if err != nil {
		return trace.Wrap(err)
	}
	interrupt.AddStopper(peer)
	return trace.Wrap(peer.Run(listener))
}

// restartInstallOrJoin restarts the install operation on installer node or
// resumes agent on the joining node.
func restartInstallOrJoin(env *localenv.LocalEnvironment) error {
	env.PrintStep("Resuming installer")

	err := InstallerClient(env, installerclient.Config{
		ConnectStrategy: &installerclient.ResumeStrategy{},
		Lifecycle: &installerclient.AutomaticLifecycle{
			Aborter:            installerAbortOperation(env),
			Completer:          InstallerCompleteOperation(env),
			DebugReportPath:    DebugReportPath(),
			LocalDebugReporter: InstallerGenerateLocalReport(env),
		},
	})
	if utils.IsContextCancelledError(err) {
		// We only end up here if the initialization has not been successful - clean up the state
		if err := InstallerCleanup(); err != nil {
			log.Warnf("Failed to clean up installer: %v.", err)
		}
		return trace.Wrap(err, "installer interrupted")
	}
	return trace.Wrap(err)
}

// clientInterruptSignals lists signals installer client considers interrupts
var clientInterruptSignals = signals.WithSignals(
	os.Interrupt,
)

// clientTerminationHandler implements the default interrupt handler for the installer client
func clientTerminationHandler(interrupt *signals.InterruptHandler, printer utils.Printer) {
	const abortTimeout = 5 * time.Second
	var timerC <-chan time.Time
	// number of consecutive interrupts
	var interrupts int
	for {
		select {
		case sig := <-interrupt.C:
			// Interrupt signaled
			interrupts += 1
			if interrupts > 1 {
				printer.Println("Received", sig, "signal. Aborting the installer gracefully, please wait.")
				interrupt.Abort()
				return
			}
			printer.Println("Press Ctrl+C again to abort the installation.")
			timerC = time.After(abortTimeout)
		case <-timerC:
			// If the interrupt signal is not re-triggered within the allotted time,
			// the signal is dropped
			interrupts = 0
			timerC = nil
		case <-interrupt.Done():
			return
		}
	}
}

type leaveConfig struct {
	force     bool
	confirmed bool
}

func leave(env *localenv.LocalEnvironment, c leaveConfig) error {
	err := tryLeave(env, c)
	if err != nil {
		if !c.force || isCancelledError(err) {
			return trace.Wrap(err)
		}
		log.WithError(err).Warn("Failed to leave cluster, forcing.")
		err := systemUninstall(env, true)
		if err != nil {
			return trace.Wrap(err)
		}
	}
	return nil
}

func tryLeave(env *localenv.LocalEnvironment, c leaveConfig) error {
	if err := checkRunningAsRoot(); err != nil {
		return trace.Wrap(err)
	}

	err := httplib.InGravity(env.DNS.Addr())
	if err != nil {
		return trace.NotFound(
			"no running cluster detected, please use --force flag to clean up the local state")
	}

	operator, err := env.SiteOperator()
	if err != nil {
		return trace.Wrap(err)
	}

	site, err := operator.GetLocalSite()
	if err != nil {
		return trace.Wrap(err)
	}

	server, err := findLocalServer(*site)
	if err != nil {
		return trace.NotFound(
			"this server is not a part of the running cluster, please use --force flag to clean up the local state")
	}

	if !c.confirmed {
		err = enforceConfirmation(
			"Please confirm removing %v (%v) from the cluster", server.Hostname, server.AdvertiseIP)
		if err != nil {
			return trace.Wrap(err)
		}
	}

	err = remove(env, removeConfig{
		server:    server.Hostname,
		confirmed: true,
		force:     c.force,
	})
	if err != nil {
		return trace.BadParameter(
			"error launching shrink operation, please use --force flag to force delete: %v", err)
	}

	return nil
}

func remove(env *localenv.LocalEnvironment, c removeConfig) error {
	if err := checkRunningAsRoot(); err != nil {
		return trace.Wrap(err)
	}

	if err := c.checkAndSetDefaults(); err != nil {
		return trace.Wrap(err)
	}

	operator, err := env.SiteOperator()
	if err != nil {
		return trace.Wrap(err)
	}

	site, err := operator.GetLocalSite()
	if err != nil {
		return trace.Wrap(err)
	}

	server, err := findServer(*site, []string{c.server})
	if err != nil {
		return trace.Wrap(err)
	}

	if !c.confirmed {
		err = enforceConfirmation(
			"Please confirm removing %v (%v) from the cluster", server.Hostname, server.AdvertiseIP)
		if err != nil {
			return trace.Wrap(err)
		}
	}

	key, err := operator.CreateSiteShrinkOperation(context.TODO(),
		ops.CreateSiteShrinkOperationRequest{
			AccountID:  site.AccountID,
			SiteDomain: site.Domain,
			Servers:    []string{server.Hostname},
			Force:      c.force,
		})
	if err != nil {
		return trace.Wrap(err)
	}

	fmt.Printf("launched operation %q, use 'gravity status' to poll its progress\n", key.OperationID)
	return nil
}

func autojoin(env *localenv.LocalEnvironment, environ LocalEnvironmentFactory, d autojoinConfig) (err error) {
	if d.fromService {
		return autojoinFromService(env, environ, d)
	}

	err = retryUpdateJoinConfigFromCloudMetadata(context.TODO(), &d)
	if err != nil {
		return trace.Wrap(err)
	}
	if err := d.checkAndSetDefaults(); err != nil {
		return trace.Wrap(err)
	}

	env.PrintStep("Auto-joining cluster %q via %v\n", d.clusterName, d.serviceURL)

	config := d.newJoinConfig()
	strategy, err := newAutoAgentConnectStrategy(env, config)
	if err != nil {
		return trace.Wrap(err)
	}
	err = joinClient(env, installerclient.Config{
		ConnectStrategy: strategy,
		Lifecycle: &installerclient.AutomaticLifecycle{
			Aborter:   installerAbortOperation(env),
			Completer: InstallerCompleteOperation(env),
		},
	})
	if utils.IsContextCancelledError(err) {
		// We only end up here if the initialization has not been successful - clean up the state
		if err := InstallerCleanup(); err != nil {
			log.Warnf("Failed to clean up installer: %v.", err)
		}
		return trace.Wrap(err, "agent interrupted")
	}
	return trace.Wrap(err)
}

func autojoinFromService(env *localenv.LocalEnvironment, environ LocalEnvironmentFactory, config autojoinConfig) error {
	if err := config.checkAndSetDefaults(); err != nil {
		return trace.Wrap(err)
	}
	joinEnv, err := environ.NewJoinEnv()
	if err != nil {
		return trace.Wrap(err)
	}
	defer joinEnv.Close()
	return trace.Wrap(joinFromService(env, joinEnv, config.newJoinConfig()))
}

func agent(env *localenv.LocalEnvironment, config agentConfig) error {
	if err := config.checkAndSetDefaults(); err != nil {
		return trace.Wrap(err)
	}

	serviceUser, err := install.EnsureServiceUserAndBinary(config.serviceUID, config.serviceGID)
	if err != nil {
		return trace.Wrap(err)
	}
	log.Infof("Service user: %+v.", serviceUser)

	gravityPath, err := exec.LookPath(defaults.GravityBin)
	if err != nil {
		return trace.Wrap(err, "failed to lookup gravity binary")
	}

	if config.serviceName != "" {
		command := config.newServiceArgs(gravityPath)
		req := systemservice.NewServiceRequest{
			ServiceSpec: systemservice.ServiceSpec{
				StartCommand: strings.Join(command, " "),
			},
			NoBlock: true,
			Name:    config.serviceName,
		}
		log.WithField("req", req).Info("Installing service with req.")
		err := service.ReinstallOneshot(req)
		if err != nil {
			return trace.Wrap(err)
		}
		env.Printf("Agent service %v started.\n", config.serviceName)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	interrupt := signals.NewInterruptHandler(ctx, cancel, InterruptSignals)
	defer interrupt.Close()
	go TerminationHandler(interrupt, env)

	creds, err := loadRPCCredentials(ctx, config.packageAddr, config.token)
	if err != nil {
		return trace.Wrap(err)
	}

	runtimeConfig := pb.RuntimeConfig{
		Token:     config.token,
		KeyValues: config.vars,
	}
	watchCh := make(chan rpcserver.WatchEvent, 1)
	agent, err := install.NewAgent(install.AgentConfig{
		FieldLogger:   log.WithField("addr", config.advertiseAddr),
		AdvertiseAddr: config.advertiseAddr,
		CloudProvider: config.cloudProvider,
		Credentials:   *creds,
		ServerAddr:    config.serverAddr,
		RuntimeConfig: runtimeConfig,
		WatchCh:       watchCh,
	})
	if err != nil {
		return trace.Wrap(err)
	}

	interrupt.AddStopper(agent)
	watchReconnects(ctx, cancel, watchCh)

	return trace.Wrap(agent.Serve())
}

func executeInstallPhase(env *localenv.LocalEnvironment, params PhaseParams, operation *ops.SiteOperation) error {
	return trace.Wrap(executePhaseFromService(
		env, params, operation, "Connecting to installer", "Connected to installer"))
}

func rollbackInstallPhase(env *localenv.LocalEnvironment, params PhaseParams, operation *ops.SiteOperation) error {
	return trace.Wrap(rollbackPhaseFromService(
		env, params, operation, "Connecting to installer", "Connected to installer"))
}

func completeInstallPlan(env *localenv.LocalEnvironment, operation *ops.SiteOperation) error {
	return trace.Wrap(completePlanFromService(
		env, operation, "Connecting to installer", "Connected to installer"))
}

func executeJoinPhase(env *localenv.LocalEnvironment, params PhaseParams, operation *ops.SiteOperation) error {
	return trace.Wrap(executePhaseFromService(
		env, params, operation, "Connecting to agent", "Connected to agent"))
}

func rollbackJoinPhase(env *localenv.LocalEnvironment, params PhaseParams, operation *ops.SiteOperation) error {
	return trace.Wrap(rollbackPhaseFromService(
		env, params, operation, "Connecting to agent", "Connected to agent"))
}

func completeJoinPlan(env *localenv.LocalEnvironment, operation *ops.SiteOperation) error {
	return trace.Wrap(completePlanFromService(
		env, operation, "Connecting to agent", "Connected to agent"))
}

func setPhaseFromService(env *localenv.LocalEnvironment, params SetPhaseParams, operation *ops.SiteOperation) error {
	ctx, cancel := context.WithCancel(context.Background())
	interrupt := signals.NewInterruptHandler(ctx, cancel, clientInterruptSignals)
	defer interrupt.Close()
	go clientTerminationHandler(interrupt, env)
	client, err := installerclient.New(ctx, installerclient.Config{
		InterruptHandler: interrupt,
		ConnectStrategy:  &installerclient.ResumeStrategy{},
	})
	if err != nil {
		return trace.Wrap(err)
	}
	return client.SetPhase(context.Background(), installerclient.Phase{
		Key: operation.Key(),
		ID:  params.PhaseID,
	}, params.State)
}

func executePhaseFromService(
	env *localenv.LocalEnvironment,
	params PhaseParams,
	operation *ops.SiteOperation,
	connecting, connected string,
) error {
	ctx, cancel := context.WithCancel(context.Background())
	interrupt := signals.NewInterruptHandler(ctx, cancel, clientInterruptSignals)
	defer interrupt.Close()
	go clientTerminationHandler(interrupt, env)

	env.PrintStep(connecting)
	config := installerclient.Config{
		ConnectStrategy:  &installerclient.ResumeStrategy{},
		InterruptHandler: interrupt,
		Printer:          env,
	}
	if params.isResume() {
		config.Lifecycle = &installerclient.AutomaticLifecycle{
			Aborter:            installerAbortOperation(env),
			Completer:          InstallerCompleteOperation(env),
			DebugReportPath:    DebugReportPath(),
			LocalDebugReporter: InstallerGenerateLocalReport(env),
		}
	}
	client, err := installerclient.New(ctx, config)
	if err != nil {
		return trace.Wrap(err)
	}
	env.PrintStep(connected)
	phaseCtx, phaseCancel := context.WithTimeout(context.Background(), params.Timeout)
	defer phaseCancel()
	return trace.Wrap(client.ExecutePhase(phaseCtx, installerclient.Phase{
		ID:    params.PhaseID,
		Force: params.Force,
		Key:   operation.Key(),
	}))
}

func rollbackPhaseFromService(
	env *localenv.LocalEnvironment,
	params PhaseParams,
	operation *ops.SiteOperation,
	connecting, connected string,
) error {
	ctx, cancel := context.WithCancel(context.Background())
	interrupt := signals.NewInterruptHandler(ctx, cancel, clientInterruptSignals)
	defer interrupt.Close()
	go clientTerminationHandler(interrupt, env)

	env.PrintStep(connecting)
	client, err := installerclient.New(ctx, installerclient.Config{
		InterruptHandler: interrupt,
		Printer:          env,
		ConnectStrategy:  &installerclient.ResumeStrategy{},
	})
	if err != nil {
		return trace.Wrap(err)
	}
	env.PrintStep(connected)
	phaseCtx, phaseCancel := context.WithTimeout(context.Background(), params.Timeout)
	defer phaseCancel()
	return trace.Wrap(client.RollbackPhase(phaseCtx, installerclient.Phase{
		ID:    params.PhaseID,
		Force: params.Force,
		Key:   operation.Key(),
	}))
}

func completePlanFromService(
	env *localenv.LocalEnvironment,
	operation *ops.SiteOperation,
	connecting, connected string,
) error {
	ctx, cancel := context.WithCancel(context.Background())
	interrupt := signals.NewInterruptHandler(ctx, cancel, clientInterruptSignals)
	defer interrupt.Close()
	go clientTerminationHandler(interrupt, env)

	env.PrintStep(connecting)
	client, err := installerclient.New(ctx, installerclient.Config{
		InterruptHandler: interrupt,
		Printer:          env,
		ConnectStrategy:  &installerclient.ResumeStrategy{},
		Lifecycle: &installerclient.AutomaticLifecycle{
			Completer: InstallerCompleteOperation(env),
		},
	})
	if err != nil {
		return trace.Wrap(err)
	}
	env.PrintStep(connected)
	return trace.Wrap(client.Complete(context.Background(), operation.Key()))
}

// InstallerClient runs the client for the installer service.
// The client is responsible for triggering the install operation and observing
// operation progress
func InstallerClient(env *localenv.LocalEnvironment, config installerclient.Config) error {
	printInstallInstructionsBanner(env)
	return trace.Wrap(installerClient(env, config, "Connecting to installer", "Connected to installer"))
}

// join executes the join command and runs either the client or the service depending on the configuration
func join(env *localenv.LocalEnvironment, environ LocalEnvironmentFactory, config JoinConfig) error {
	env.PrintStep("Starting agent")

	if err := config.CheckAndSetDefaults(); err != nil {
		return trace.Wrap(err)
	}
	if config.FromService {
		joinEnv, err := environ.NewJoinEnv()
		if err != nil {
			return trace.Wrap(err)
		}
		defer joinEnv.Close()
		return trace.Wrap(joinFromService(env, joinEnv, config))
	}
	strategy, err := newAgentConnectStrategy(env, config)
	if err != nil {
		return trace.Wrap(err)
	}
	err = joinClient(env, installerclient.Config{
		ConnectStrategy: strategy,
		Lifecycle: &installerclient.AutomaticLifecycle{
			Aborter:            installerAbortOperation(env),
			Completer:          InstallerCompleteOperation(env),
			DebugReportPath:    DebugReportPath(),
			LocalDebugReporter: InstallerGenerateLocalReport(env),
		},
	})
	if utils.IsContextCancelledError(err) {
		// We only end up here if the initialization has not been successful - clean up the state
		if err := InstallerCleanup(); err != nil {
			log.Warnf("Failed to clean up installer: %v.", err)
		}
		return trace.Wrap(err, "agent interrupted")
	}
	return trace.Wrap(err)
}

// TerminationHandler implements the default interrupt handler for the installer service
func TerminationHandler(interrupt *signals.InterruptHandler, printer utils.Printer) {
	for {
		select {
		case sig := <-interrupt.C:
			log.Info("Received ", sig, " signal. Terminating the installer gracefully, please wait.")
			interrupt.Abort()
			return
		case <-interrupt.Done():
			return
		}
	}
}

// NewServiceListener returns a new listener for the installer service
func NewServiceListener() (net.Listener, error) {
	socketPath, err := installpb.SocketPath()
	if err != nil {
		return nil, trace.Wrap(err)
	}
	if err := os.RemoveAll(socketPath); err != nil {
		return nil, trace.Wrap(trace.ConvertSystemError(err), "failed to remove installer socket")
	}
	return net.Listen("unix", socketPath)
}

// InterruptSignals lists signals installer service considers interrupts
var InterruptSignals = signals.WithSignals(
	os.Interrupt,
	syscall.SIGTERM,
	syscall.SIGQUIT,
)

// NewInstallerConnectStrategy returns default installer service connect strategy
func NewInstallerConnectStrategy(env *localenv.LocalEnvironment, config InstallConfig, commandArgs cli.CommandArgs) (strategy installerclient.ConnectStrategy, err error) {
	args, err := commandArgs.Update(os.Args[1:], cli.NewFlag("token", config.Token))
	if err != nil {
		return nil, trace.Wrap(err)
	}
	args = append([]string{utils.Exe.Path}, args...)
	args = append(args, "--from-service", utils.Exe.WorkingDir)
	servicePath, err := state.GravityInstallDir(defaults.GravityRPCInstallerServiceName)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return &installerclient.InstallerStrategy{
		Args:           args,
		Validate:       environ.ValidateInstall(env),
		ApplicationDir: utils.Exe.WorkingDir,
		ServicePath:    servicePath,
	}, nil
}

// newReconfiguratorConnectStrategy returns a new service connect strategy
// for the agent executing the cluster reconfiguration operation.
func newReconfiguratorConnectStrategy(env *localenv.LocalEnvironment, config InstallConfig, commandArgs cli.CommandArgs) (strategy installerclient.ConnectStrategy, err error) {
	args, err := commandArgs.Update(os.Args[1:])
	if err != nil {
		return nil, trace.Wrap(err)
	}
	args = append([]string{utils.Exe.Path}, args...)
	args = append(args, "--from-service")
	servicePath, err := state.GravityInstallDir(defaults.GravityRPCInstallerServiceName)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return &installerclient.InstallerStrategy{
		Args:           args,
		Validate:       func() error { return nil },
		ApplicationDir: utils.Exe.WorkingDir,
		ServicePath:    servicePath,
	}, nil
}

// newAutoAgentConnectStrategy returns a new service connect strategy for a joining agent
// in autojoin scenario
func newAutoAgentConnectStrategy(env *localenv.LocalEnvironment, config JoinConfig) (strategy installerclient.ConnectStrategy, err error) {
	// TODO: accept command line parser as argument if the join command
	// is to be extended on enterprise side
	commandArgs := cli.CommandArgs{
		Parser: cli.ArgsParserFunc(parseArgs),
		// Pass additional configuration to service if not explicitly specified.
		FlagsToAdd: []cli.Flag{
			cli.NewFlag("token", config.Token),
			cli.NewFlag("advertise-addr", config.AdvertiseAddr),
			cli.NewFlag("service-addr", config.PeerAddrs),
		},
	}
	args, err := commandArgs.Update(os.Args[1:])
	if err != nil {
		return nil, trace.Wrap(err)
	}
	args = append([]string{utils.Exe.Path}, args...)
	args = append(args, "--from-service")
	servicePath, err := state.GravityInstallDir(defaults.GravityRPCAgentServiceName)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return &installerclient.InstallerStrategy{
		Args:           args,
		Validate:       environ.ValidateInstall(env),
		ApplicationDir: utils.Exe.WorkingDir,
		ServicePath:    servicePath,
	}, nil
}

// newAgentConnectStrategy returns default service connect strategy for a joining agent
func newAgentConnectStrategy(env *localenv.LocalEnvironment, config JoinConfig) (strategy installerclient.ConnectStrategy, err error) {
	args := append([]string{utils.Exe.Path}, os.Args[1:]...)
	args = append(args, "--from-service")
	servicePath, err := state.GravityInstallDir(defaults.GravityRPCAgentServiceName)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return &installerclient.InstallerStrategy{
		Args:           args,
		Validate:       environ.ValidateInstall(env),
		ApplicationDir: utils.Exe.WorkingDir,
		ServicePath:    servicePath,
	}, nil
}

func installerClient(env *localenv.LocalEnvironment, config installerclient.Config, connecting, connected string) error {
	// Context to use for cancelling tasks before initialization is complete
	ctx, cancel := context.WithCancel(context.Background())
	interrupt := signals.NewInterruptHandler(ctx, cancel, clientInterruptSignals)
	defer interrupt.Close()
	go clientTerminationHandler(interrupt, env)

	config.InterruptHandler = interrupt
	config.Printer = env
	env.PrintStep(connecting)
	client, err := installerclient.New(ctx, config)
	if err != nil {
		return trace.Wrap(err)
	}
	env.PrintStep(connected)
	return trace.Wrap(client.Run(context.Background()))
}

func printInstallInstructionsBanner(printer utils.Printer) {
	printer.Println(color.YellowString(`
To abort the installation and clean up the system,
press Ctrl+C two times in a row.

If you get disconnected from the terminal, you can reconnect to the installer
agent by issuing 'gravity resume' command.

If the installation fails, use 'gravity plan' to inspect the state and
'gravity resume' to continue the operation.
See https://gravitational.com/gravity/docs/cluster/#managing-an-ongoing-operation for details.
`))
}

func printJoinInstructionsBanner(printer utils.Printer) {
	printer.Println(color.YellowString(`
To abort the agent and clean up the system,
press Ctrl+C two times in a row.

If you get disconnected from the terminal, you can reconnect to the installer
agent by issuing 'gravity resume' command.
See https://gravitational.com/gravity/docs/cluster/#managing-an-ongoing-operation for details.
`))
}

// DebugReportPath returns the default path for the debug report file
func DebugReportPath() (path string) {
	return filepath.Join(filepath.Dir(utils.Exe.Path), defaults.DebugReportFile)
}
