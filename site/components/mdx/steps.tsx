export function Steps({ children }: { children: React.ReactNode }) {
  return <div className="steps">{children}</div>;
}

export function Step({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="step">
      <p className="font-semibold text-fg">{title}</p>
      {children}
    </div>
  );
}
