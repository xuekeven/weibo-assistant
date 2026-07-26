import assert from 'node:assert/strict'
import test from 'node:test'
import { createLatestRequestGate } from './requestGate.ts'

test('only the latest request token can commit', () => {
  const gate = createLatestRequestGate()
  const first = gate.begin('group-1')
  const second = gate.begin('group-2')

  assert.equal(gate.isCurrent(first, 'group-1'), false)
  assert.equal(gate.isCurrent(second, 'group-2'), true)
  assert.equal(gate.isCurrent(second, 'group-1'), false)
})

test('invalidate prevents an in-flight request from committing', () => {
  const gate = createLatestRequestGate()
  const request = gate.begin('filters')
  gate.invalidate()

  assert.equal(gate.isCurrent(request, 'filters'), false)
})
