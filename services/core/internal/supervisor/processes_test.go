package supervisor

import (
	"cloudforge/core/internal/model"
	"cloudforge/core/internal/persistence"
	"cloudforge/core/internal/projects"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func logged(logs []model.Log, text string) bool {
	for _, l := range logs {
		if strings.Contains(l.Message, text) {
			return true
		}
	}
	return false
}

func TestAdditionalProcessLifecycle(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node not installed")
	}
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not installed")
	}
	root := t.TempDir()
	for name, content := range map[string]string{
		"package.json": `{"name":"process-fixture","version":"1.0.0","private":true,"scripts":{"start":"node server.js"}}`,
		"server.js":    `console.log('main-alive'); setInterval(()=>{}, 1000);`,
		"worker.js":    `console.log('worker-alive', process.argv.slice(2).join(' '), process.env.SHARED_VALUE, process.env.PORT || 'no-port'); setInterval(()=>{}, 1000);`,
		".env":         "SHARED_VALUE=from-dotenv\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	dbPath := filepath.Join(t.TempDir(), "test.db")
	logDir := t.TempDir()
	store, err := persistence.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	p := model.Project{Name: "Main", Runtime: "node", Path: root, RestartPolicy: "on-failure", StartCommand: []string{"node", "server.js"}}
	if err = projects.New(&p); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	sup := New(store, logDir)
	sup.Add(p)
	if _, err = sup.AddProcess(p.ID, model.Process{Name: "Shell", StartCommand: []string{"cmd", "/c", "node", "worker.js"}}); err == nil {
		t.Fatal("shell command accepted")
	}
	proc, err := sup.AddProcess(p.ID, model.Process{Name: "Notifier", StartCommand: []string{"node", "worker.js", "--notify"}})
	if err != nil {
		t.Fatal(err)
	}
	if proc.ID == "" || proc.DesiredState != "stopped" || proc.RestartPolicy != "on-failure" {
		t.Fatalf("process not initialized: %+v", proc)
	}
	if _, err = sup.Action(proc.ID, "start"); err == nil {
		t.Fatal("project action accepted a process ID")
	}
	if _, err = sup.ProcessAction("other", proc.ID, "start"); err == nil {
		t.Fatal("process action accepted the wrong project")
	}
	// Start both together: the install lock must keep them from preparing at the same time.
	if _, err = sup.Action(p.ID, "start"); err != nil {
		t.Fatal(err)
	}
	if _, err = sup.ProcessAction(p.ID, proc.ID, "start"); err != nil {
		t.Fatal(err)
	}
	view := func() (model.View, model.ProcessView) {
		v := sup.Views()
		if len(v) != 1 {
			t.Fatalf("expected one project view, got %d", len(v))
		}
		if len(v[0].Processes) != 1 {
			return v[0], model.ProcessView{}
		}
		return v[0], v[0].Processes[0]
	}
	await(t, 90*time.Second, func() bool {
		main, extra := view()
		return main.RuntimeInfo.ActualState == "online" && extra.RuntimeInfo.ActualState == "online"
	})
	main, extra := view()
	if main.RuntimeInfo.PID == extra.RuntimeInfo.PID || extra.RuntimeInfo.PID == 0 {
		t.Fatal("process did not get its own PID")
	}
	await(t, 15*time.Second, func() bool {
		logs, _ := sup.ProcessLogs(p.ID, proc.ID)
		return logged(logs, "worker-alive --notify from-dotenv no-port")
	})
	mainLogs, _ := sup.Logs(p.ID)
	if logged(mainLogs, "worker-alive") {
		t.Fatal("process output leaked into the project's logs")
	}
	if _, err = sup.ProcessLogs("other", proc.ID); err == nil {
		t.Fatal("logs served for the wrong project")
	}
	stored, _ := store.Project(p.ID)
	if len(stored.Processes) != 1 || stored.Processes[0].DesiredState != "running" {
		t.Fatalf("desired state not persisted: %+v", stored.Processes)
	}
	// Changing the project's port must keep the process record intact.
	if err = sup.SetPort(p.ID, 0); err != nil {
		t.Fatal(err)
	}
	if stored, _ = store.Project(p.ID); len(stored.Processes) != 1 {
		t.Fatal("project change dropped the process")
	}
	if _, err = sup.UpdateProcess(p.ID, proc.ID, model.Process{Name: "Notifier", StartCommand: []string{"node", "worker.js", "--changed"}, RestartPolicy: "always"}); err != nil {
		t.Fatal(err)
	}
	// A restarted Core brings the process back because its desired state is running.
	sup.Shutdown()
	if stored, _ = store.Project(p.ID); stored.Processes[0].DesiredState != "running" {
		t.Fatal("shutdown changed the desired state")
	}
	sup = New(store, logDir)
	if err = sup.Restore(); err != nil {
		t.Fatal(err)
	}
	defer sup.Shutdown()
	await(t, 60*time.Second, func() bool {
		logs, _ := sup.ProcessLogs(p.ID, proc.ID)
		return logged(logs, "worker-alive --changed")
	})
	if _, err = sup.ProcessAction(p.ID, proc.ID, "stop"); err != nil {
		t.Fatal(err)
	}
	await(t, 15*time.Second, func() bool {
		_, extra := view()
		return extra.RuntimeInfo.ActualState == "offline"
	})
	if _, main = view(); main.RuntimeInfo.ActualState != "online" {
		t.Fatal("stopping the process affected the project")
	}
	if err = sup.RemoveProcess(p.ID, proc.ID); err != nil {
		t.Fatal(err)
	}
	if m, _ := view(); len(m.Processes) != 0 {
		t.Fatal("removed process still listed")
	}
	if stored, _ = store.Project(p.ID); len(stored.Processes) != 0 {
		t.Fatal("removed process still stored")
	}
	if _, err = os.Stat(filepath.Join(root, "worker.js")); err != nil {
		t.Fatal("removing a process touched project files")
	}
	again, err := sup.AddProcess(p.ID, model.Process{Name: "Second", StartCommand: []string{"node", "worker.js"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sup.ProcessAction(p.ID, again.ID, "start"); err != nil {
		t.Fatal(err)
	}
	await(t, 30*time.Second, func() bool {
		_, extra := view()
		return extra.RuntimeInfo.ActualState == "online"
	})
	if err = sup.Remove(p.ID); err != nil {
		t.Fatal(err)
	}
	if len(sup.Views()) != 0 || len(sup.processJobs(p.ID)) != 0 {
		t.Fatal("project removal left processes behind")
	}
}
