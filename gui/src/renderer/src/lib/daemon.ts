import { initTransport } from './grpc-client'

export async function connectToDaemon(): Promise<{ host: string; port: number }> {
  const info = await window.watchfire.getDaemonInfo()
  if (!info) {
    throw new Error('Daemon is not running')
  }

  initTransport(info.port, info.host || 'localhost')
  return { host: info.host || 'localhost', port: info.port }
}
