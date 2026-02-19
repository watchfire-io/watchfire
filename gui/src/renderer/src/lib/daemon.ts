import { initTransport } from './grpc-client'

export async function connectToDaemon(): Promise<{ host: string; port: number }> {
  const info = await window.watchfire.ensureDaemon()

  initTransport(info.port, info.host || 'localhost')
  return { host: info.host || 'localhost', port: info.port }
}
