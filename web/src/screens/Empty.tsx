export function Empty({ title, body }: { title: string; body: string }) {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-1 px-6 text-center">
      <h2 className="text-lead font-semibold text-text">{title}</h2>
      <p className="text-body text-muted">{body}</p>
    </div>
  );
}

export function Terminals() {
  return <Empty title="No terminals yet." body="Link a runner to open your first terminal." />;
}
