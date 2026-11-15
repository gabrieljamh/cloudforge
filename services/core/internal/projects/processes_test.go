package projects

import (
	"cloudforge/core/internal/model"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateProcess(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{
		"package.json": `{"scripts":{"start":"node server.js","notify":"node admin.js --notify"}}`,
		"server.js":    "",
		"admin.js":     "",
		"worker.py":    "",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	node := model.Project{ID: "p", Name: "App", Runtime: "node", Path: root, WorkingDirectory: root, Port: 8080, StartCommand: []string{"node", "server.js"},
		Processes: []model.Process{{ID: "a", Name: "Existing", Port: 9000, StartCommand: []string{"node", "admin.js"}}}}
	valid := []model.Process{
		{Name: "Notifier", StartCommand: []string{"node", "admin.js", "--notify"}},
		{Name: "Script", StartCommand: []string{"npm", "run", "notify"}, Port: 9001, RestartPolicy: "always"},
		{ID: "a", Name: "existing", Port: 9000, StartCommand: []string{"node", "admin.js"}},
	}
	for _, proc := range valid {
		if err := ValidateProcess(node, &proc); err != nil {
			t.Fatalf("%s: %v", proc.Name, err)
		}
		if proc.RestartPolicy == "" || proc.EntryPoint != "" || len(proc.Arguments) != 0 {
			t.Fatalf("%s not normalized: %+v", proc.Name, proc)
		}
	}
	invalid := map[string]model.Process{
		"shell":          {Name: "Shell", StartCommand: []string{"bash", "-c", "node admin.js"}},
		"missing file":   {Name: "Missing", StartCommand: []string{"node", "nope.js"}},
		"missing script": {Name: "Missing script", StartCommand: []string{"npm", "run", "nope"}},
		"outside":        {Name: "Outside", StartCommand: []string{"node", "../server.js"}},
		"main port":      {Name: "Port", StartCommand: []string{"node", "admin.js"}, Port: 8080},
		"sibling port":   {Name: "Port", StartCommand: []string{"node", "admin.js"}, Port: 9000},
		"duplicate":      {Name: "EXISTING", StartCommand: []string{"node", "admin.js"}},
		"no name":        {Name: "  ", StartCommand: []string{"node", "admin.js"}},
		"policy":         {Name: "Policy", StartCommand: []string{"node", "admin.js"}, RestartPolicy: "sometimes"},
	}
	for name, proc := range invalid {
		if err := ValidateProcess(node, &proc); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	python := model.Project{ID: "py", Name: "Bot", Runtime: "python", Path: root, WorkingDirectory: root, EntryPoint: filepath.Join(root, "worker.py")}
	proc := model.Process{Name: "Worker", EntryPoint: "worker.py", Arguments: []string{"--once"}, StartCommand: []string{"node", "server.js"}}
	if err := ValidateProcess(python, &proc); err != nil {
		t.Fatal(err)
	}
	if proc.EntryPoint != filepath.Join(root, "worker.py") || len(proc.StartCommand) != 0 || len(proc.Arguments) != 1 {
		t.Fatalf("python process not normalized: %+v", proc)
	}
	bad := model.Process{Name: "Text", EntryPoint: "package.json"}
	if err := ValidateProcess(python, &bad); err == nil {
		t.Fatal("non-Python entry accepted")
	}
	derived := ProcessProject(node, valid[0])
	if derived.ID != node.ID || derived.Path != root || derived.Port != 0 || derived.TerminalEnabled || derived.Processes != nil {
		t.Fatalf("derived project wrong: %+v", derived)
	}
}
