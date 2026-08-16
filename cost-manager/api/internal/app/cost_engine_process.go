package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"cost-manager/api/pkg/logger"
)

// costEngineProcess owns exactly one deploy-unit workflow: establish the
// isolated Engine identity, supervise the child, and expose its readiness.
// It contains no HTTP, database, Redis, pricing, or wallet capability.
type costEngineProcess struct {
	commandPath string
	environment []string
	readyFile   string
	command     *exec.Cmd
	done        chan error
	running     atomic.Bool
}

func newCostEngineProcess(environment []string) (*costEngineProcess, error) {
	engineEnvironment, err := buildCostEngineEnvironment(environment)
	if err != nil {
		return nil, err
	}
	commandPath := "cost-manager-engine"
	if _, err := exec.LookPath(commandPath); err != nil {
		commandPath = "../engine/target/release/cost-manager-engine"
		if _, err := os.Stat(commandPath); err != nil {
			commandPath = "../engine/target/debug/cost-manager-engine"
		}
	}
	readyFile := ""
	for _, item := range environment {
		name, value, found := strings.Cut(item, "=")
		if found && name == "COST_ENGINE_READY_FILE" {
			readyFile = strings.TrimSpace(value)
			break
		}
	}
	if readyFile == "" {
		readyFile = filepath.Join(
			os.TempDir(),
			"aurora-cost-engine-"+strconv.Itoa(os.Getpid())+".ready",
		)
	}
	return &costEngineProcess{
		commandPath: commandPath,
		environment: engineEnvironment,
		readyFile:   readyFile,
	}, nil
}

func (process *costEngineProcess) Start() error {
	if process == nil {
		return errors.New("Cost Engine process is required")
	}
	if err := os.Remove(process.readyFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale Cost Engine readiness marker: %w", err)
	}
	process.command = exec.Command(process.commandPath)
	process.command.Stdout = os.Stdout
	process.command.Stderr = os.Stderr
	process.command.Env = append(
		append([]string(nil), process.environment...),
		"AURORA_ENGINE_READY_FILE="+process.readyFile,
	)
	if err := process.command.Start(); err != nil {
		return fmt.Errorf("start embedded Cost Engine: %w", err)
	}
	process.done = make(chan error, 1)
	process.running.Store(true)
	go func() {
		err := process.command.Wait()
		process.running.Store(false)
		_ = os.Remove(process.readyFile)
		process.done <- err
		close(process.done)
	}()
	return nil
}

func (process *costEngineProcess) Done() <-chan error {
	if process == nil {
		return nil
	}
	return process.done
}

func (process *costEngineProcess) Ready() bool {
	if process == nil || !process.running.Load() {
		return false
	}
	_, err := os.Stat(process.readyFile)
	return err == nil
}

func (process *costEngineProcess) Stop() {
	if process == nil {
		return
	}
	if process.command != nil && process.command.Process != nil && process.running.Load() {
		logger.SysInfo("cost_engine_process.stop", "Terminating Rust Engine child process...")
		_ = process.command.Process.Signal(syscall.SIGTERM)
		select {
		case <-process.done:
			logger.SysInfo("cost_engine_process.stop", "Rust Engine terminated.")
		case <-time.After(15 * time.Second):
			logger.SysWarn("cost_engine_process.stop", "Rust Engine graceful shutdown timed out; killing child process")
			_ = process.command.Process.Kill()
			<-process.done
		}
	}
	_ = os.Remove(process.readyFile)
}

// The embedded Engine is a distinct Vault principal. This private boundary is
// intentionally testable because leaking the API token into the charge runtime
// would collapse the two security identities.
func buildCostEngineEnvironment(environment []string) ([]string, error) {
	values := make(map[string]string, len(environment))
	for _, item := range environment {
		name, value, found := strings.Cut(item, "=")
		if found {
			values[name] = strings.TrimSpace(value)
		}
	}
	token := values["VAULT_ENGINE_TOKEN"]
	roleID := values["VAULT_ENGINE_ROLE_ID"]
	secretID := values["VAULT_ENGINE_SECRET_ID"]
	kubernetesRole := values["VAULT_ENGINE_KUBERNETES_ROLE"]
	if token == "" && !((roleID != "" && secretID != "") || kubernetesRole != "") {
		return nil, errors.New("an isolated Cost Engine Vault token, AppRole, or Kubernetes role is required")
	}

	result := make([]string, 0, len(environment)+5)
	for _, item := range environment {
		name, _, found := strings.Cut(item, "=")
		if !found {
			continue
		}
		if strings.HasPrefix(name, "VAULT_ENGINE_") || name == "VAULT_TOKEN" ||
			name == "VAULT_ROLE_ID" || name == "VAULT_SECRET_ID" ||
			name == "VAULT_KUBERNETES_ROLE" || name == "VAULT_KUBERNETES_JWT_PATH" {
			continue
		}
		result = append(result, item)
	}
	if token != "" {
		result = append(result, "VAULT_TOKEN="+token)
	} else if roleID != "" && secretID != "" {
		result = append(result, "VAULT_ROLE_ID="+roleID, "VAULT_SECRET_ID="+secretID)
	} else {
		result = append(result, "VAULT_KUBERNETES_ROLE="+kubernetesRole)
		if jwtPath := values["VAULT_ENGINE_KUBERNETES_JWT_PATH"]; jwtPath != "" {
			result = append(result, "VAULT_KUBERNETES_JWT_PATH="+jwtPath)
		}
	}
	return result, nil
}
