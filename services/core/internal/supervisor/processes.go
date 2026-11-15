package supervisor

import (
	"cloudforge/core/internal/auth"
	"cloudforge/core/internal/model"
	"cloudforge/core/internal/projects"
	"errors"
	"slices"
	"time"
)

// AddProcess registers an additional process for a project. It starts stopped.
func (s *Supervisor) AddProcess(projectID string, proc model.Process) (model.Process, error) {
	parent, err := s.project(projectID)
	if err != nil {
		return proc, err
	}
	parent.save.Lock()
	defer parent.save.Unlock()
	parent.mu.Lock()
	if parent.removing {
		parent.mu.Unlock()
		return proc, errors.New("projeto em exclusão ou já excluído")
	}
	p := parent.p
	parent.mu.Unlock()
	if len(p.Processes) >= projects.MaxProcesses {
		return proc, errors.New("limite de processos adicionais atingido")
	}
	proc.ID = ""
	if err = projects.ValidateProcess(p, &proc); err != nil {
		return proc, err
	}
	now := time.Now().UTC()
	proc.ID, proc.DesiredState, proc.CreatedAt, proc.UpdatedAt = auth.ID(), "stopped", now, now
	p.Processes = append(slices.Clone(p.Processes), proc)
	p.UpdatedAt = now
	if err = s.store.SaveProject(p); err != nil {
		return proc, err
	}
	parent.mu.Lock()
	parent.p = p
	parent.mu.Unlock()
	s.addProcessJob(p, proc)
	return proc, nil
}

// UpdateProcess changes a process's name, launch command, port or restart policy.
// A running process keeps its current command until its next start.
func (s *Supervisor) UpdateProcess(projectID, processID string, input model.Process) (model.Process, error) {
	var saved model.Process
	err := s.updateProcess(projectID, processID, func(parent model.Project, proc *model.Process) error {
		next := *proc
		next.Name, next.EntryPoint, next.StartCommand, next.Arguments = input.Name, input.EntryPoint, input.StartCommand, input.Arguments
		next.Port, next.RestartPolicy = input.Port, input.RestartPolicy
		if err := projects.ValidateProcess(parent, &next); err != nil {
			return err
		}
		next.UpdatedAt = time.Now().UTC()
		*proc = next
		saved = next
		return nil
	})
	return saved, err
}

// RemoveProcess stops a process, removes its logs and unregisters it. Project files stay.
func (s *Supervisor) RemoveProcess(projectID, processID string) error {
	x, err := s.processJob(projectID, processID)
	if err != nil {
		return err
	}
	parent, err := s.project(projectID)
	if err != nil {
		return err
	}
	if !x.op.TryLock() {
		return errors.New("aguarde a operação em andamento neste processo")
	}
	defer x.op.Unlock()
	x.mu.Lock()
	if x.removing {
		x.mu.Unlock()
		return errors.New("processo em exclusão ou já excluído")
	}
	x.removing = true
	x.p.DesiredState = "stopped"
	x.mu.Unlock()
	deleted := false
	defer func() {
		if !deleted {
			x.mu.Lock()
			x.removing = false
			x.mu.Unlock()
		}
	}()
	s.stop(x)
	parent.save.Lock()
	defer parent.save.Unlock()
	parent.mu.Lock()
	p := parent.p
	parent.mu.Unlock()
	p.Processes = slices.DeleteFunc(slices.Clone(p.Processes), func(proc model.Process) bool { return proc.ID == processID })
	p.UpdatedAt = time.Now().UTC()
	if err = s.store.SaveProject(p); err != nil {
		return err
	}
	parent.mu.Lock()
	parent.p = p
	parent.mu.Unlock()
	_ = x.logs.Remove()
	s.mu.Lock()
	delete(s.jobs, processID)
	s.mu.Unlock()
	deleted = true
	return nil
}

// ProcessLogs returns the current run's logs of one additional process.
func (s *Supervisor) ProcessLogs(projectID, processID string) ([]model.Log, error) {
	x, err := s.processJob(projectID, processID)
	if err != nil {
		return nil, err
	}
	return x.logs.Lines(), nil
}

// updateProcess applies a change to one process inside the project's record, saves the
// record and refreshes the command the process's job will run on its next attempt.
func (s *Supervisor) updateProcess(projectID, processID string, change func(model.Project, *model.Process) error) error {
	parent, err := s.project(projectID)
	if err != nil {
		return err
	}
	x, err := s.processJob(projectID, processID)
	if err != nil {
		return err
	}
	parent.save.Lock()
	defer parent.save.Unlock()
	parent.mu.Lock()
	p := parent.p
	parent.mu.Unlock()
	p.Processes = slices.Clone(p.Processes)
	index := slices.IndexFunc(p.Processes, func(proc model.Process) bool { return proc.ID == processID })
	if index < 0 {
		return errors.New("processo não encontrado")
	}
	if err = change(p, &p.Processes[index]); err != nil {
		return err
	}
	if err = s.store.SaveProject(p); err != nil {
		return err
	}
	parent.mu.Lock()
	parent.p = p
	parent.mu.Unlock()
	x.mu.Lock()
	x.p = projects.ProcessProject(p, p.Processes[index])
	x.mu.Unlock()
	return nil
}
