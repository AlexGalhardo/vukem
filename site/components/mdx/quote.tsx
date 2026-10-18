/** A quotation with its source: author (optional) and a link to where it comes from. */
export function Quote({
  source,
  author,
  title,
  children,
}: {
  source: string;
  author?: string;
  title?: string;
  children: React.ReactNode;
}): React.ReactElement {
  const label = title ?? hostOf(source);
  return (
    <figure className="quote">
      <blockquote cite={source}>{children}</blockquote>
      <figcaption>
        {"— "}
        {author ? <>{author}, </> : null}
        <a href={source} target="_blank" rel="noreferrer">
          {label}
        </a>
      </figcaption>
    </figure>
  );
}

function hostOf(url: string): string {
  try {
    const u = new URL(url);
    return `${u.hostname.replace(/^www\./, "")}${u.pathname === "/" ? "" : u.pathname}`;
  } catch {
    return url;
  }
}
