import { LOG_CHOICES, type LogSettings, type ProcessingSettings } from "../../api/index.ts";
import { LoadState, Row, SaveBar, Switch } from "./parts.tsx";
import { useSettingsForm } from "./useSettingsForm.ts";

const dayLabel = (n: number) => (n === 0 ? "Forever" : `${n} days`);

function DaySelect({ id, label, value, values, onChange }: { id: string; label: string; value: number; values: readonly number[]; onChange: (n: number) => void }) {
  return (
    <select id={id} aria-label={label} className="sel" value={value} onChange={(e) => onChange(Number(e.target.value))}>
      {values.map((v) => (
        <option key={v} value={v}>
          {dayLabel(v)}
        </option>
      ))}
    </select>
  );
}

export function Processing() {
  const f = useSettingsForm("processing", ["processing", "logs"]);
  const draft = f.draft;
  if (!draft) return <LoadState s={f.s} />;
  const p = draft.processing;
  const l = draft.logs;
  const setP = (patch: Partial<ProcessingSettings>) => f.edit({ ...draft, processing: { ...p, ...patch } });
  const setL = (patch: Partial<LogSettings>) => f.edit({ ...draft, logs: { ...l, ...patch } });
  return (
    <>
      <h2 className="set-title">Processing</h2>
      <Row id="p-par" label="Files at once" hint="How many files convert at the same time, across all workers." field="processing.parallelJobs" problem={f.problem}>
        <input id="p-par" type="number" className="inp" min={1} max={8} value={p.parallelJobs} onChange={(e) => setP({ parallelJobs: e.target.valueAsNumber })} />
      </Row>
      <Row label="Graphics-card jobs separate" hint="Files that need the graphics card don't share a worker with other files.">
        <Switch label="Graphics-card jobs separate" on={p.gpuJobsSeparate} onChange={(gpuJobsSeparate) => setP({ gpuJobsSeparate })} />
      </Row>
      <Row id="p-scan" label="Daily scan time" hint="Leave empty for no daily scan. Libraries that watch for changes still do." field="processing.scanSchedule" problem={f.problem}>
        <input id="p-scan" className="inp" placeholder="02:00" value={p.scanSchedule} onChange={(e) => setP({ scanSchedule: e.target.value })} />
      </Row>
      <Row id="p-queue" label="Next file" hint="Which waiting file starts next.">
        <select id="p-queue" className="sel" value={p.queueOrder} onChange={(e) => setP({ queueOrder: e.target.value as ProcessingSettings["queueOrder"] })}>
          <option value="newest">Newest first</option>
          <option value="oldest">Oldest first</option>
          <option value="largest">Largest first</option>
          <option value="smallest">Smallest first</option>
        </select>
      </Row>
      <Row label="Low priority" hint="Conversions yield to other work on the machine.">
        <Switch label="Low priority" on={p.lowPriority} onChange={(lowPriority) => setP({ lowPriority })} />
      </Row>
      <Row id="p-keep" label="Keep originals (days)" hint="Originals wait in the trash this many days after the new file passes. While any workflow replaces originals, keep at least 7. Otherwise 0 deletes them straight away." field="processing.keepOriginalsDays" problem={f.problem}>
        <input id="p-keep" type="number" className="inp" min={0} max={365} value={p.keepOriginalsDays} onChange={(e) => setP({ keepOriginalsDays: e.target.valueAsNumber })} />
      </Row>
      <Row id="p-space" label="Low disk space limit (GB)" hint="A library disk below this gets no new jobs." field="processing.lowSpaceGB" problem={f.problem}>
        <input id="p-space" type="number" className="inp" min={1} max={1000} value={p.lowSpaceGB} onChange={(e) => setP({ lowSpaceGB: e.target.valueAsNumber })} />
      </Row>
      <Row label="Workflows may run commands" hint="Lets workflows run commands on this server. Off unless you need it.">
        <Switch label="Workflows may run commands" on={p.workflowsMayRunCommands} onChange={(workflowsMayRunCommands) => setP({ workflowsMayRunCommands })} />
      </Row>
      <Row label="Trash folder" hint="Where removed originals wait. Set by the server.">
        <span className="mono sm">{p.trashPath}</span>
      </Row>

      <h3 className="set-sub">Logs</h3>
      <Row id="l-job" label="Job logs kept" hint="How long each job's log is kept. Forever keeps it." field="logs.jobDays" problem={f.problem}>
        <DaySelect id="l-job" label="Job logs kept" value={l.jobDays} values={LOG_CHOICES.jobDays} onChange={(jobDays) => setL({ jobDays })} />
      </Row>
      <Row id="l-sys" label="System log kept" hint="The System log has its own timeline, separate from job logs." field="logs.systemDays" problem={f.problem}>
        <DaySelect id="l-sys" label="System log kept" value={l.systemDays} values={LOG_CHOICES.systemDays} onChange={(systemDays) => setL({ systemDays })} />
      </Row>
      <Row id="l-raw" label="Raw tool output kept" hint="Output from the tools, for debugging. It can't outlast its job log." field="logs.rawDays" problem={f.problem}>
        <DaySelect id="l-raw" label="Raw tool output kept" value={l.rawDays} values={LOG_CHOICES.rawDays} onChange={(rawDays) => setL({ rawDays })} />
      </Row>
      <SaveBar dirty={f.dirty} busy={f.busy} saved={f.saved} text={f.text} onSave={f.save} onUndo={f.undo} />
    </>
  );
}
