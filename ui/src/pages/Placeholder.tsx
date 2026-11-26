import { Link } from "../router.tsx";

// Routes whose screens are in later batches. Shows the name, not a fake screen.
export function Placeholder({ title, note }: { title: string; note?: string }) {
  return (
    <>
      <div className="pagehead">
        <h1>{title}</h1>
      </div>
      <section className="card">
        <p className="muted">{note ?? "Not built yet. This screen comes in a later batch."}</p>
        <p>
          <Link to="/">Back to Dashboard</Link>
        </p>
      </section>
    </>
  );
}
