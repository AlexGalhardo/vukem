// A meta refresh survives a static host. redirect() becomes a client-only
// Next payload, which does not run until the page's scripts load.
export function StaticRedirect({ href }: { href: string }): React.ReactElement {
  return (
    <>
      <meta httpEquiv="refresh" content={`0;url=${href}`} />
      <p className="px-4 py-16 text-center text-sm">
        <a href={href}>Continue</a>
      </p>
    </>
  );
}
