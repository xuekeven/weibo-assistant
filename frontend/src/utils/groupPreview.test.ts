import assert from 'node:assert/strict'
import test from 'node:test'
import { shouldAdvanceGroupPreview } from './groupPreview.ts'

test('older image updates cannot replace the newest group preview', () => {
  assert.equal(shouldAdvanceGroupPreview('2026-10-10T18:19:34+08:00', '2026-10-10T12:27:00+08:00'), false)
})

test('equal timestamps keep the existing preview', () => {
  assert.equal(shouldAdvanceGroupPreview('2026-10-10T18:19:34+08:00', '2026-10-10T18:19:34+08:00'), false)
})

test('newer messages advance the preview and empty groups get a first preview', () => {
  assert.equal(shouldAdvanceGroupPreview('2026-10-10T18:19:34+08:00', '2026-10-10T18:20:00+08:00'), true)
  assert.equal(shouldAdvanceGroupPreview(undefined, '2026-10-10T18:20:00+08:00'), true)
})
