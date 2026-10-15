import { releaseReason, uuidV7, workspaceKey } from './presentation.ts'
import type { WorkspaceVolume } from './model.ts'

// One key per confirmed intent; an uncertain result must never create a new operation.
export function createReleaseIntents(makeKey = uuidV7) {
  const keys = new Map<string, string>()
  return {
    key(target: WorkspaceVolume, confirmation: string) {
      if (releaseReason(target) || confirmation !== target.pvc_name) throw new Error('RELEASE_NOT_CONFIRMED')
      const identity = `${workspaceKey(target)}/${target.pv_name}`
      if (!keys.has(identity)) keys.set(identity, makeKey())
      return keys.get(identity)!
    },
    clear() { keys.clear() },
  }
}
export function isReleased(value: unknown, target: WorkspaceVolume) {
  if (!value || typeof value !== 'object') return false
  const v = value as { status?: string; workspace?: Partial<WorkspaceVolume> & { state?: string } }
  return v.status === 'released' && v.workspace?.namespace === target.namespace &&
    v.workspace?.pvc_name === target.pvc_name && v.workspace?.pv_name === target.pv_name
}
