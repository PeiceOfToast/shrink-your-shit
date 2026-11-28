import { sampleOnly, type Group } from "../api/index.ts";

// A page or panel whose calls have no server route yet runs on the mock's made-up data. In the real build it says so,
// so sample numbers never read as real ones. group: the route group that would connect it (none: not planned yet).
export function SampleBanner({ group, text = "this page isn't connected yet." }: { group?: Group; text?: string }) {
  if (!sampleOnly(group)) return null;
  return (
    <div className="note" role="note" style={{ margin: "0 0 12px" }}>
      <span>
        <b>Sample data:</b> {text}
      </span>
    </div>
  );
}
