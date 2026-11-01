// Shared public-contract checks only; compatible with Node and k6.
export function stateFromBody(body, noteId) {
  const s = body && body.success === true && body.data
  if (!s || s.noteId !== noteId || typeof s.liked !== 'boolean' ||
      !Number.isSafeInteger(s.version) || s.version < 0 ||
      !Number.isSafeInteger(s.likes) || s.likes < 0) return null
  return { liked: s.liked, version: s.version, likes: s.likes }
}

export function classifyWrite(status, body, noteId, previous, wanted) {
  if (status === 0 || status >= 500) return { kind: 'uncertain', state: null }
  if (status !== 200) return { kind: 'rejected', state: null }
  const state = stateFromBody(body, noteId)
  if (!state || state.liked !== wanted) return { kind: 'invalid', state }
  if (state.version === previous.version + 1 && wanted !== previous.liked) return { kind: 'mutation', state }
  if (state.version === previous.version) return { kind: 'noop', state }
  return { kind: 'invalid', state }
}

export function pairForVU(fixture, mode, vu) {
  if (!Number.isInteger(vu) || vu < 1 || vu > fixture.users.length) throw new Error('VU outside exclusive relation pool')
  const user = fixture.users[vu - 1]
  const noteId = mode === 'hot' ? fixture.hotNoteId : fixture.multiNoteIds[vu - 1]
  const initial = fixture.initial[vu - 1]
  if (!initial || initial.userId !== user.userId || initial.noteId !== noteId) throw new Error('relation initial fact mismatch')
  return { user, noteId, initial: { liked: initial.active === 1, version: initial.version } }
}

export function unknownBounds(attempted, completed, effectUnknown) {
  if (![attempted, completed, effectUnknown].every(x => Number.isSafeInteger(x) && x >= 0) ||
      completed > attempted || effectUnknown > attempted) throw new Error('invalid uncertainty counters')
  const classificationUnknown = attempted - completed
  const unknownLowerBound = Math.max(classificationUnknown, effectUnknown)
  const unknownUpperBound = Math.min(attempted, classificationUnknown + effectUnknown)
  const unknownIsExact = unknownLowerBound === unknownUpperBound
  return { classificationUnknown, unknownLowerBound, unknownUpperBound, unknownIsExact,
    unknown: unknownIsExact ? unknownLowerBound : null }
}
