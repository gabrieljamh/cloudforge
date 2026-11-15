package api

import (
	"cloudforge/core/internal/model"
	"errors"
	"net/http"
)

// processLocal serves the additional processes of a project (desktop administration only):
//
//	POST   /api/projects/{id}/processes               create
//	PATCH  /api/projects/{id}/processes/{pid}         edit (applies on next start)
//	DELETE /api/projects/{id}/processes/{pid}         stop and remove
//	POST   /api/projects/{id}/processes/{pid}/action  start, stop or restart
//	GET    /api/projects/{id}/processes/{pid}/logs    current run's logs
func (s *Server) processLocal(w http.ResponseWriter, r *http.Request, projectID string, parts []string) {
	if _, err := s.Store.Project(projectID); err != nil {
		fail(w, 404, errors.New("projeto não encontrado"))
		return
	}
	type input struct {
		Name          string   `json:"name"`
		EntryPoint    string   `json:"entryPoint"`
		StartCommand  []string `json:"startCommand"`
		Arguments     []string `json:"arguments"`
		Port          int      `json:"port"`
		RestartPolicy string   `json:"restartPolicy"`
	}
	read := func() (model.Process, bool) {
		var b input
		if err := decode(w, r, &b); err != nil {
			fail(w, 400, err)
			return model.Process{}, false
		}
		return model.Process{Name: b.Name, EntryPoint: b.EntryPoint, StartCommand: b.StartCommand, Arguments: b.Arguments, Port: b.Port, RestartPolicy: b.RestartPolicy}, true
	}
	switch {
	case len(parts) == 0 && r.Method == http.MethodPost:
		proc, ok := read()
		if !ok {
			return
		}
		created, err := s.Supervisor.AddProcess(projectID, proc)
		if err != nil {
			fail(w, 400, err)
			return
		}
		write(w, 201, created)
	case len(parts) == 1 && r.Method == http.MethodPatch:
		proc, ok := read()
		if !ok {
			return
		}
		saved, err := s.Supervisor.UpdateProcess(projectID, parts[0], proc)
		if err != nil {
			fail(w, 400, err)
			return
		}
		write(w, 200, saved)
	case len(parts) == 1 && r.Method == http.MethodDelete:
		if err := s.Supervisor.RemoveProcess(projectID, parts[0]); err != nil {
			fail(w, 409, err)
			return
		}
		write(w, 200, map[string]bool{"ok": true})
	case len(parts) == 2 && parts[1] == "action" && r.Method == http.MethodPost:
		var b struct {
			Action string `json:"action"`
		}
		if err := decode(w, r, &b); err != nil {
			fail(w, 400, err)
			return
		}
		op, err := s.Supervisor.ProcessAction(projectID, parts[0], b.Action)
		if err != nil {
			fail(w, 409, err)
			return
		}
		write(w, 202, map[string]string{"operationId": op})
	case len(parts) == 2 && parts[1] == "logs" && r.Method == http.MethodGet:
		logs, err := s.Supervisor.ProcessLogs(projectID, parts[0])
		if err != nil {
			fail(w, 404, err)
			return
		}
		write(w, 200, logs)
	default:
		http.NotFound(w, r)
	}
}
