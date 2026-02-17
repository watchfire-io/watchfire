import { createGrpcWebTransport } from '@connectrpc/connect-web'
import { createClient, type Client, type Transport } from '@connectrpc/connect'
import {
  ProjectService,
  TaskService,
  DaemonService,
  AgentService,
  LogService,
  BranchService,
  SettingsService
} from '../generated/watchfire_pb'

let transport: Transport | null = null

export function initTransport(port: number, host = 'localhost'): void {
  transport = createGrpcWebTransport({
    baseUrl: `http://${host}:${port}`
  })
}

export function getTransport(): Transport {
  if (!transport) throw new Error('gRPC transport not initialized')
  return transport
}

export function getProjectClient(): Client<typeof ProjectService> {
  return createClient(ProjectService, getTransport())
}

export function getTaskClient(): Client<typeof TaskService> {
  return createClient(TaskService, getTransport())
}

export function getDaemonClient(): Client<typeof DaemonService> {
  return createClient(DaemonService, getTransport())
}

export function getAgentClient(): Client<typeof AgentService> {
  return createClient(AgentService, getTransport())
}

export function getLogClient(): Client<typeof LogService> {
  return createClient(LogService, getTransport())
}

export function getBranchClient(): Client<typeof BranchService> {
  return createClient(BranchService, getTransport())
}

export function getSettingsClient(): Client<typeof SettingsService> {
  return createClient(SettingsService, getTransport())
}
