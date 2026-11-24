import type { FeedPage, LikeState, Note, NoteSearchPage, Relationship } from '../types'
import { actors, fail, ok, readDemo, shops, writeDemo, type DemoRequest } from './support'

interface Flag {
  active: boolean
  version: number
}

interface Community {
  notes: Note[]
  likes: Record<string, Flag>
  follows: Record<string, Flag>
}

const key = 'community'
const initialCommunity = (): Community => {
  const now = new Date().toISOString()
  return {
    notes: [
      {
        id: '1',
        shopId: '1',
        userId: actors.customer.id,
        title: '街角咖啡的周末手冲',
        content: '周末逛完街区，在街角咖啡点了一杯手冲。靠窗桌采光很好，适合坐下来读一会儿书。',
        images: [],
        liked: 0,
        comments: 0,
        status: 'PUBLISHED',
        publishVersion: 1,
        createTime: now,
        updateTime: now,
      },
    ],
    likes: {},
    follows: {},
  }
}

function countActive(flags: Record<string, Flag>, target: string): number {
  return Object.entries(flags).filter(([pair, flag]) => flag.active && pair.endsWith(':' + target))
    .length
}

function getNote(state: Community, note: Note): Note {
  return { ...note, liked: countActive(state.likes, note.id) }
}

function readFlag(flags: Record<string, Flag>, pair: string): Flag {
  return flags[pair] ?? { active: false, version: 0 }
}

function updateFlag(flags: Record<string, Flag>, pair: string, active: boolean): Flag {
  const previous = readFlag(flags, pair)
  if (previous.active === active) return previous
  const flag = { active, version: previous.version + 1 }
  flags[pair] = flag
  return flag
}

function createNote(state: Community, userId: string, body: DemoRequest['body']): Response {
  const title = typeof body.title === 'string' ? body.title.trim() : ''
  const content = typeof body.content === 'string' ? body.content.trim() : ''
  const shopId = typeof body.shopId === 'string' ? body.shopId : undefined
  if (!title || title.length > 100 || !content || content.length > 2000) {
    return fail('请填写标题（最多 100 字）和正文（最多 2000 字）。', 400)
  }
  if (shopId && !shops.some((shop) => shop.id === shopId)) {
    return fail('请选择演示门店，可输入 1（街角咖啡）、2（青禾食堂）或 3（城市书房）。', 400)
  }
  if (!Array.isArray(body.images) || body.images.length) {
    return fail('本演示支持发布文字笔记，请将图片地址留空。', 400)
  }

  const now = new Date().toISOString()
  const note: Note = {
    id: String(Number(state.notes[0]?.id ?? 0) + 1),
    userId,
    title,
    content,
    shopId,
    images: [],
    liked: 0,
    comments: 0,
    status: 'PUBLISHED',
    publishVersion: 1,
    createTime: now,
    updateTime: now,
  }
  state.notes.unshift(note)
  writeDemo(key, state)
  return ok(note)
}

function pageLimit(query: URLSearchParams): number {
  const value = Number(query.get('limit') ?? 10)
  return Number.isInteger(value) && value > 0 ? Math.min(value, 50) : 10
}

function search(state: Community, query: URLSearchParams): Response {
  const keyword = (query.get('q') ?? '').trim().toLocaleLowerCase()
  if (!keyword || keyword.length > 100 || keyword.includes('\0')) {
    return fail('请输入 1 至 100 字的搜索关键词。', 400)
  }
  const shopId = query.get('shopId')
  const matches = state.notes.filter((note) => {
    const text = (note.title + '\n' + note.content).toLocaleLowerCase()
    return (
      (!shopId || note.shopId === shopId) &&
      keyword.split(/\s+/).every((word) => text.includes(word))
    )
  })
  if (query.get('sort') !== 'recent') {
    const titleScore = (note: Note) => Number(note.title.toLocaleLowerCase().includes(keyword))
    matches.sort((a, b) => titleScore(b) - titleScore(a))
  }
  const cursor = query.get('cursor')
  const offset = cursor ? matches.findIndex((note) => note.id === cursor) + 1 : 0
  if (cursor && !offset) return fail('分页已失效，请重新搜索。', 400)
  const limit = pageLimit(query)
  const items = matches.slice(offset, offset + limit)
  const page: NoteSearchPage = {
    items,
    nextCursor: matches.length > offset + limit ? items.at(-1)?.id : undefined,
  }
  return ok(page)
}

function feed(state: Community, userId: string, query: URLSearchParams): Response {
  const cursor = query.get('cursorId')
  const matches = state.notes.filter(
    (note) =>
      readFlag(state.follows, userId + ':' + note.userId).active &&
      (!cursor || Number(note.id) < Number(cursor)),
  )
  const limit = pageLimit(query)
  const items = matches.slice(0, limit).map((note) => getNote(state, note))
  const last = items.at(-1)
  const page: FeedPage = {
    items,
    nextCursor:
      matches.length > limit && last ? { id: last.id, publishedAt: last.createTime } : undefined,
  }
  return ok(page)
}

export function handleCommunity({
  path,
  method,
  query,
  body,
  user,
}: DemoRequest): Response | undefined {
  const noteRoute = /^\/notes\/(\d+)(\/like)?$/.exec(path)
  const authorRoute = /^\/authors\/(\d+)\/(follow|relationship)$/.exec(path)
  if (!noteRoute && !authorRoute && !['/notes', '/notes/search', '/feed/notes'].includes(path)) {
    return undefined
  }

  const state = readDemo(key, initialCommunity)
  if (path === '/notes/search' && method === 'GET') return search(state, query)

  const note = noteRoute ? state.notes.find(({ id }) => id === noteRoute[1]) : undefined
  if (noteRoute && !note) return fail('笔记不存在。', 404)
  if (note && !noteRoute?.[2] && method === 'GET') return ok(getNote(state, note))
  if (!user) return fail('请先选择演示身份。', 401)

  if (path === '/notes' && method === 'POST') return createNote(state, user.id, body)

  if (note && noteRoute?.[2] && ['GET', 'PUT'].includes(method)) {
    const pair = user.id + ':' + note.id
    if (method === 'PUT') {
      if (typeof body.liked !== 'boolean') return fail('无法识别点赞操作。', 400)
      updateFlag(state.likes, pair, body.liked)
      writeDemo(key, state)
    }
    const flag = readFlag(state.likes, pair)
    const result: LikeState = {
      noteId: note.id,
      liked: flag.active,
      version: flag.version,
      likes: countActive(state.likes, note.id),
    }
    return ok(result)
  }

  if (authorRoute) {
    const [, authorId, action] = authorRoute
    if (!authorId || !Object.values(actors).some((actor) => actor.id === authorId)) {
      return fail('作者不存在。', 404)
    }
    const pair = user.id + ':' + authorId
    if (action === 'follow' && method === 'PUT') {
      if (authorId === user.id || typeof body.follow !== 'boolean') {
        return fail('无法关注此作者。', 400)
      }
      updateFlag(state.follows, pair, body.follow)
      writeDemo(key, state)
    } else if (action !== 'relationship' || method !== 'GET') {
      return fail('此演示请求不受支持。', 405)
    }
    const flag = readFlag(state.follows, pair)
    const result: Relationship = {
      authorId,
      following: flag.active,
      version: flag.version,
      followerCount: countActive(state.follows, authorId),
    }
    return ok(result)
  }

  if (path === '/feed/notes' && method === 'GET') return feed(state, user.id, query)
  return fail('此演示请求不受支持。', 405)
}
