interface Props {
  version: string
}

export function AboutSection({ version }: Props) {
  return (
    <section>
      <h3 className="font-heading font-semibold text-sm text-[var(--wf-text-muted)] uppercase tracking-wider mb-3">
        About
      </h3>
      <div className="text-sm text-[var(--wf-text-secondary)]">
        <span className="text-[var(--wf-text-primary)] font-medium">Watchfire</span>{' '}
        {version && <span>v{version}</span>}
      </div>
    </section>
  )
}
