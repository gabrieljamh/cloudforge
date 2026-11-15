package api

import (
	"cloudforge/core/internal/model"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestProcessRoutes(t *testing.T) {
	s := fixture(t)
	if err := s.Auth.Bootstrap("admin", "fixture-long-password"); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"package.json": `{"scripts":{"start":"node server.js"}}`, "server.js": "", "admin.js": ""} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	p := model.Project{ID: "proj", Name: "App", Runtime: "node", Path: root, WorkingDirectory: root, DesiredState: "stopped", Port: 8080, StartCommand: []string{"node", "server.js"}}
	if err = s.Store.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	s.Supervisor.Add(p)
	base := "/api/projects/proj/processes"
	if w := invoke(s, true, "POST", base, `{"name":"Notifier","startCommand":["node","admin.js","--notify"],"port":8080}`, "", "", nil); w.Code != 400 {
		t.Fatalf("port collision accepted: %d %s", w.Code, w.Body)
	}
	if w := invoke(s, true, "POST", base, `{"name":"Notifier","startCommand":["sh","-c","node admin.js"]}`, "", "", nil); w.Code != 400 {
		t.Fatalf("shell accepted: %d", w.Code)
	}
	w := invoke(s, true, "POST", base, `{"name":"Notifier","startCommand":["node","admin.js","--notify"]}`, "", "", nil)
	if w.Code != 201 {
		t.Fatalf("create failed: %d %s", w.Code, w.Body)
	}
	var created model.Process
	if err = json.Unmarshal(w.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("bad create response: %s", w.Body)
	}
	if w = invoke(s, true, "PATCH", base+"/"+created.ID, `{"name":"Alerts","startCommand":["node","admin.js","--watch"],"port":9100,"restartPolicy":"always"}`, "", "", nil); w.Code != 200 {
		t.Fatalf("update failed: %d %s", w.Code, w.Body)
	}
	stored, _ := s.Store.Project("proj")
	if len(stored.Processes) != 1 || stored.Processes[0].Name != "Alerts" || stored.Processes[0].Port != 9100 {
		t.Fatalf("update not stored: %+v", stored.Processes)
	}
	if w = invoke(s, true, "GET", base+"/"+created.ID+"/logs", "", "", "", nil); w.Code != 200 {
		t.Fatalf("logs failed: %d", w.Code)
	}
	if w = invoke(s, true, "POST", base+"/"+created.ID+"/action", `{"action":"explode"}`, "", "", nil); w.Code != 409 {
		t.Fatalf("invalid action accepted: %d", w.Code)
	}
	if w = invoke(s, true, "POST", "/api/projects/"+created.ID+"/action", `{"action":"start"}`, "", "", nil); w.Code != 404 {
		t.Fatalf("process ID accepted as a project: %d", w.Code)
	}
	if w = invoke(s, true, "GET", "/api/projects/missing/processes/"+created.ID+"/logs", "", "", "", nil); w.Code != 404 {
		t.Fatalf("unknown project accepted: %d", w.Code)
	}
	var state map[string]any
	w = invoke(s, true, "GET", "/api/projects", "", "", "", nil)
	_ = json.Unmarshal([]byte(`{"projects":`+w.Body.String()+`}`), &state)
	list, _ := state["projects"].([]any)
	if len(list) != 1 || len(list[0].(map[string]any)["processes"].([]any)) != 1 {
		t.Fatalf("project view does not list the process: %s", w.Body)
	}
	if w = invoke(s, true, "DELETE", base+"/"+created.ID, "", "", "", nil); w.Code != 200 {
		t.Fatalf("delete failed: %d %s", w.Code, w.Body)
	}
	if stored, _ = s.Store.Project("proj"); len(stored.Processes) != 0 {
		t.Fatal("process still stored after delete")
	}
	if w = invoke(s, true, "DELETE", base+"/"+created.ID, "", "", "", nil); w.Code != 409 {
		t.Fatalf("second delete: %d", w.Code)
	}
}
