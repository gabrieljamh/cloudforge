import { useState, type FormEvent } from "react";
import { Check, Pencil, Plus, Terminal, Trash2 } from "lucide-react";
import type { Project, ProjectProcess } from "../../../packages/shared-types";
import {
  Actions,
  bytes,
  ErrorMessage,
  Modal,
  percent,
  Spinner,
  Status,
  uptime,
} from "../../../packages/ui";
import { api, message } from "./api";
import { formatArguments, parseArguments } from "./Projects";
import "./project-processes.css";

const policies: Record<string, string> = {
  never: "Nunca reinicia",
  "on-failure": "Reinicia em caso de falha",
  always: "Sempre reinicia",
};

function commandLine(project: Project, process: ProjectProcess) {
  if (project.runtime === "node") return formatArguments(process.startCommand);
  const entry = process.entryPoint.startsWith(project.path)
    ? process.entryPoint.slice(project.path.length).replace(/^[\\/]/, "")
    : process.entryPoint;
  return ["python", entry, formatArguments(process.arguments)]
    .filter(Boolean)
    .join(" ");
}

export function ProjectProcesses({
  project,
  onChanged,
  onShowLogs,
}: {
  project: Project;
  onChanged: () => void;
  onShowLogs: (processId: string) => void;
}) {
  const processes = project.processes ?? [];
  const [editing, setEditing] = useState<ProjectProcess | "new" | null>(null);
  const [removing, setRemoving] = useState<ProjectProcess | null>(null);
  const [confirm, setConfirm] = useState<{
    process: ProjectProcess;
    action: string;
  } | null>(null);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");

  async function action(process: ProjectProcess, name: string) {
    setBusy(process.id);
    setError("");
    setConfirm(null);
    try {
      await api(
        "POST",
        `/api/projects/${project.id}/processes/${process.id}/action`,
        { action: name },
      );
      onChanged();
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy("");
    }
  }

  async function remove(process: ProjectProcess) {
    setBusy(process.id);
    setError("");
    try {
      await api(
        "DELETE",
        `/api/projects/${project.id}/processes/${process.id}`,
      );
      setRemoving(null);
      onChanged();
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy("");
    }
  }

  return (
    <section className="settings-section project-processes">
      <div className="project-processes-head">
        <div>
          <h2>Processos adicionais</h2>
          <p>
            Rodam na pasta do projeto, com as mesmas dependências e o mesmo
            arquivo <code>.env</code>. Cada processo tem comando, porta, logs e
            reinicialização próprios, e inicia ou para sem afetar a aplicação
            principal.
          </p>
        </div>
        <button
          className="button primary"
          disabled={processes.length >= 16}
          onClick={() => {
            setError("");
            setEditing("new");
          }}
        >
          <Plus size={16} /> Adicionar processo
        </button>
      </div>
      <ErrorMessage error={error} />
      {!processes.length ? (
        <div className="empty-state compact">
          <h3>Nenhum processo adicional</h3>
          <p>
            Use para tarefas que precisam rodar ao lado da aplicação, como um
            notificador, um worker de fila ou um bot.
          </p>
        </div>
      ) : (
        <ul className="process-list">
          {processes.map((process) => {
            const execution = process.execution;
            const online = execution.actualState === "online";
            return (
              <li key={process.id} className="process-item">
                <div className="process-main">
                  <div className="process-title">
                    <strong>{process.name}</strong>
                    <Status state={execution.actualState} />
                  </div>
                  <code className="process-command">
                    {commandLine(project, process)}
                  </code>
                  <dl className="process-meta">
                    <div>
                      <dt>Porta</dt>
                      <dd>{process.port || "Sem HTTP"}</dd>
                    </div>
                    <div>
                      <dt>CPU</dt>
                      <dd>{online ? percent(execution.cpu) : "—"}</dd>
                    </div>
                    <div>
                      <dt>RAM</dt>
                      <dd>
                        {online && execution.memory != null
                          ? bytes(execution.memory)
                          : "—"}
                      </dd>
                    </div>
                    <div>
                      <dt>Tempo ativo</dt>
                      <dd>
                        {online && execution.startedAt
                          ? uptime(execution.startedAt)
                          : "—"}
                      </dd>
                    </div>
                    <div>
                      <dt>Reinicialização</dt>
                      <dd>{policies[process.restartPolicy] ?? "—"}</dd>
                    </div>
                  </dl>
                  {execution.lastError && (
                    <p className="process-error">{execution.lastError}</p>
                  )}
                </div>
                <div className="process-controls">
                  <Actions
                    state={execution.actualState}
                    busy={busy === process.id}
                    onAction={(name) =>
                      name === "start"
                        ? void action(process, name)
                        : setConfirm({ process, action: name })
                    }
                  />
                  <div className="process-tools">
                    <button
                      className="button"
                      onClick={() => onShowLogs(process.id)}
                    >
                      <Terminal size={15} /> Logs
                    </button>
                    <button
                      className="icon-button"
                      title="Editar processo"
                      aria-label={`Editar ${process.name}`}
                      onClick={() => {
                        setError("");
                        setEditing(process);
                      }}
                    >
                      <Pencil size={15} />
                    </button>
                    <button
                      className="icon-button"
                      title="Remover processo"
                      aria-label={`Remover ${process.name}`}
                      onClick={() => {
                        setError("");
                        setRemoving(process);
                      }}
                    >
                      <Trash2 size={15} />
                    </button>
                  </div>
                </div>
              </li>
            );
          })}
        </ul>
      )}
      {editing && (
        <ProcessForm
          project={project}
          process={editing === "new" ? null : editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            onChanged();
          }}
        />
      )}
      {confirm && (
        <Modal
          title={
            confirm.action === "stop"
              ? `Parar ${confirm.process.name}?`
              : `Reiniciar ${confirm.process.name}?`
          }
          onClose={() => setConfirm(null)}
        >
          <p>
            {confirm.action === "stop"
              ? "O processo será encerrado. A aplicação principal continua rodando."
              : "O processo será encerrado e iniciado novamente. A aplicação principal não é afetada."}
          </p>
          <div className="form-footer">
            <button className="button" onClick={() => setConfirm(null)}>
              Cancelar
            </button>
            <button
              className="button primary"
              onClick={() => void action(confirm.process, confirm.action)}
            >
              {confirm.action === "stop" ? "Parar processo" : "Reiniciar"}
            </button>
          </div>
        </Modal>
      )}
      {removing && (
        <Modal
          title={`Remover ${removing.name}?`}
          onClose={() => busy !== removing.id && setRemoving(null)}
          closeDisabled={busy === removing.id}
        >
          <p>
            O processo será parado e seu cadastro e logs serão removidos. Os
            arquivos do projeto não são alterados.
          </p>
          <div className="form-footer">
            <button
              className="button"
              disabled={busy === removing.id}
              onClick={() => setRemoving(null)}
            >
              Cancelar
            </button>
            <button
              className="button danger-quiet"
              disabled={busy === removing.id}
              onClick={() => void remove(removing)}
            >
              {busy === removing.id ? <Spinner /> : <Trash2 size={15} />}{" "}
              Remover processo
            </button>
          </div>
        </Modal>
      )}
    </section>
  );
}

function ProcessForm({
  project,
  process,
  onClose,
  onSaved,
}: {
  project: Project;
  process: ProjectProcess | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const node = project.runtime === "node";
  const relativeEntry = (entry: string) =>
    entry.startsWith(project.path)
      ? entry.slice(project.path.length).replace(/^[\\/]/, "")
      : entry;
  const [values, setValues] = useState({
    name: process?.name ?? "",
    command: process ? formatArguments(process.startCommand) : "",
    entry: process ? relativeEntry(process.entryPoint) : "",
    args: process ? formatArguments(process.arguments) : "",
    port: process?.port ? String(process.port) : "",
    policy: process?.restartPolicy ?? "on-failure",
  });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const running =
    process != null && process.execution.actualState !== "offline";

  function edit(field: keyof typeof values, value: string) {
    setValues((current) => ({ ...current, [field]: value }));
    setError("");
  }

  async function save(event: FormEvent) {
    event.preventDefault();
    const port = values.port.trim() ? Number(values.port) : 0;
    if (!Number.isInteger(port) || port < 0 || port > 65535) {
      setError("Informe uma porta entre 1 e 65535 ou deixe vazio.");
      return;
    }
    setBusy(true);
    setError("");
    const body = {
      name: values.name.trim(),
      startCommand: node ? parseArguments(values.command.trim()) : [],
      entryPoint: node ? "" : values.entry.trim(),
      arguments: node ? [] : parseArguments(values.args.trim()),
      port,
      restartPolicy: values.policy,
    };
    try {
      if (process)
        await api(
          "PATCH",
          `/api/projects/${project.id}/processes/${process.id}`,
          body,
        );
      else await api("POST", `/api/projects/${project.id}/processes`, body);
      onSaved();
    } catch (e) {
      setError(message(e));
      setBusy(false);
    }
  }

  return (
    <Modal
      title={process ? `Editar ${process.name}` : "Adicionar processo"}
      onClose={() => !busy && onClose()}
      closeDisabled={busy}
    >
      <form onSubmit={save}>
        <label>
          Nome
          <input
            required
            maxLength={60}
            value={values.name}
            disabled={busy}
            placeholder="Notificador"
            onChange={(e) => edit("name", e.target.value)}
          />
        </label>
        {node ? (
          <label>
            Comando de inicialização
            <input
              required
              value={values.command}
              disabled={busy}
              placeholder="node admin.js --notify"
              onChange={(e) => edit("command", e.target.value)}
            />
            <small>
              Use <code>node arquivo.js</code> com argumentos opcionais ou um
              script do <code>package.json</code>, como{" "}
              <code>npm run worker</code>. Comandos de shell não são aceitos.
            </small>
          </label>
        ) : (
          <div className="form-grid">
            <label>
              Arquivo Python
              <input
                required
                value={values.entry}
                disabled={busy}
                placeholder="worker.py"
                onChange={(e) => edit("entry", e.target.value)}
              />
            </label>
            <label>
              Argumentos
              <input
                value={values.args}
                disabled={busy}
                placeholder="--once"
                onChange={(e) => edit("args", e.target.value)}
              />
            </label>
          </div>
        )}
        <div className="form-grid">
          <label>
            Porta HTTP
            <input
              inputMode="numeric"
              value={values.port}
              disabled={busy}
              placeholder="Sem HTTP"
              onChange={(e) => edit("port", e.target.value)}
            />
            <small>
              Opcional. Quando definida, o processo recebe a variável{" "}
              <code>PORT</code>.
            </small>
          </label>
          <label>
            Reinicialização automática
            <select
              value={values.policy}
              disabled={busy}
              onChange={(e) => edit("policy", e.target.value)}
            >
              <option value="never">Nunca</option>
              <option value="on-failure">Em caso de falha</option>
              <option value="always">Sempre, exceto parada manual</option>
            </select>
          </label>
        </div>
        {running && (
          <div className="notice">
            O processo está em execução. As mudanças valem na próxima
            inicialização.
          </div>
        )}
        <ErrorMessage error={error} />
        <div className="form-footer">
          <button
            type="button"
            className="button"
            disabled={busy}
            onClick={onClose}
          >
            Cancelar
          </button>
          <button className="button primary" disabled={busy}>
            {busy ? <Spinner /> : <Check size={16} />}{" "}
            {process ? "Salvar processo" : "Adicionar processo"}
          </button>
        </div>
      </form>
    </Modal>
  );
}
