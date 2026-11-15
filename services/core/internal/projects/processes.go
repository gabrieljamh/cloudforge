package projects

import (
	"cloudforge/core/internal/model"
	"errors"
	"strings"
	"unicode/utf8"
)

// MaxProcesses limits the additional processes of a single project.
const MaxProcesses = 16

// ProcessProject derives the project a process runs as: same folder, runtime, dependencies
// and environment (the ID stays the parent's, so the dependency cache and the selected .env
// are shared), with the process's own launch command, port and restart policy.
func ProcessProject(parent model.Project, proc model.Process) model.Project {
	p := parent
	p.Name = parent.Name + " · " + proc.Name
	p.EntryPoint = proc.EntryPoint
	p.StartCommand = proc.StartCommand
	p.Arguments = proc.Arguments
	p.Port = proc.Port
	p.RestartPolicy = proc.RestartPolicy
	p.DesiredState = proc.DesiredState
	p.TerminalEnabled = false
	p.Processes = nil
	return p
}

// ValidateProcess checks a process against its parent project. Launch commands follow the
// same rules as the project's own: node/npm/pnpm/yarn or a .py file, never a shell.
func ValidateProcess(parent model.Project, proc *model.Process) error {
	proc.Name = strings.TrimSpace(proc.Name)
	if proc.Name == "" || utf8.RuneCountInString(proc.Name) > 60 {
		return errors.New("nome do processo deve ter entre 1 e 60 caracteres")
	}
	if proc.RestartPolicy == "" {
		proc.RestartPolicy = "on-failure"
	}
	if proc.RestartPolicy != "never" && proc.RestartPolicy != "on-failure" && proc.RestartPolicy != "always" {
		return errors.New("política de reinicialização inválida")
	}
	if proc.Port < 0 || proc.Port > 65535 {
		return errors.New("porta inválida")
	}
	if proc.Port > 0 {
		if proc.Port == parent.Port {
			return errors.New("a porta já é usada pelo processo principal")
		}
		for _, other := range parent.Processes {
			if other.ID != proc.ID && other.Port == proc.Port {
				return errors.New("a porta já é usada pelo processo " + other.Name)
			}
		}
	}
	for _, other := range parent.Processes {
		if other.ID != proc.ID && strings.EqualFold(other.Name, proc.Name) {
			return errors.New("já existe um processo com esse nome")
		}
	}
	if proc.StartCommand == nil {
		proc.StartCommand = []string{}
	}
	if proc.Arguments == nil {
		proc.Arguments = []string{}
	}
	p := ProcessProject(parent, *proc)
	if err := ValidateStartup(&p); err != nil {
		return err
	}
	if parent.Runtime == "node" {
		proc.EntryPoint, proc.Arguments = "", []string{}
	} else {
		proc.EntryPoint, proc.StartCommand = p.EntryPoint, []string{}
	}
	return nil
}
