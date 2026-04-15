// Global error handlers must be installed before any other module is
// evaluated, so a throw during store/module initialization surfaces in the
// DOM instead of leaving the user with a blank window. ErrorBoundary only
// catches errors inside the React tree — it cannot catch module-top failures
// that happen before ReactDOM.createRoot is called. Static `import` bindings
// are hoisted, so we install the handlers here and then dynamically import
// the rest of the app below.
function renderFatalError(label: string, err: unknown): void {
  const root = document.getElementById('root')
  if (!root) return
  const message = err instanceof Error ? `${err.message}\n\n${err.stack ?? ''}` : String(err)
  root.innerHTML = `
    <div style="
      font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
      color: #f5b096;
      background: #16181d;
      padding: 32px;
      min-height: 100vh;
      box-sizing: border-box;
    ">
      <h1 style="color: #e07040; margin: 0 0 16px; font-size: 18px;">Watchfire failed to start</h1>
      <p style="color: #a8a8b0; margin: 0 0 16px;"></p>
      <pre style="
        background: #1e2028;
        border: 1px solid #2a2d36;
        border-radius: 6px;
        padding: 16px;
        white-space: pre-wrap;
        word-break: break-word;
        color: #e0e0e0;
        font-size: 12px;
      "></pre>
    </div>
  `
  const p = root.querySelector('p')
  const pre = root.querySelector('pre')
  if (p) p.textContent = label
  if (pre) pre.textContent = message
}

window.addEventListener('error', (event) => {
  renderFatalError('Uncaught error during initialization:', event.error ?? event.message)
})
window.addEventListener('unhandledrejection', (event) => {
  renderFatalError('Unhandled promise rejection during initialization:', event.reason)
})

// Dynamic import so the handlers above run before any store-initialization
// side-effects in the imported module graph.
;(async () => {
  try {
    const [{ StrictMode }, { createRoot }, { default: App }, { ToastProvider }, { ErrorBoundary }] =
      await Promise.all([
        import('react'),
        import('react-dom/client'),
        import('./App'),
        import('./components/ui/Toast'),
        import('./components/ErrorBoundary')
      ])
    await import('./global.css')
    await import('@xterm/xterm/css/xterm.css')

    createRoot(document.getElementById('root')!).render(
      <StrictMode>
        <ErrorBoundary>
          <ToastProvider>
            <App />
          </ToastProvider>
        </ErrorBoundary>
      </StrictMode>
    )
  } catch (err) {
    renderFatalError('Failed to load application modules:', err)
  }
})()
