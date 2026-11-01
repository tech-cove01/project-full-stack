// Pure response contract shared by the CAP09 k6 workload and offline checks.
// No networking, credentials, mutable state or metric reporting occurs here.
export function validCap09Feed(page, reader, fixture) {
  if (!page || page.source !== 'database' || !Array.isArray(page.items) || page.items.length !== 20 || fixture.pageSize !== 20) return false
  if (!Array.isArray(reader.expectedIds) || !Array.isArray(reader.expectedTitles) || !Array.isArray(reader.expectedCreateTimes) ||
      reader.expectedIds.length !== 20 || reader.expectedTitles.length !== 20 || reader.expectedCreateTimes.length !== 20 ||
      fixture.content !== 'x'.repeat(256) || typeof fixture.shopName !== 'string') return false
  if (!page.items.every((item, index) => item && item.id === reader.expectedIds[index] && item.userId === reader.authorId &&
      item.shopId === '1' && item.shopName === fixture.shopName && item.title === reader.expectedTitles[index] &&
      item.content === fixture.content && Array.isArray(item.images) && item.images.length === 0 &&
      item.createTime === reader.expectedCreateTimes[index] && item.liked === 0 && item.comments === 0 && item.likedByMe === false)) return false
  const last = page.items[19]
  return page.nextCursor !== null && typeof page.nextCursor === 'object' &&
    page.nextCursor.id === last.id && page.nextCursor.publishedAt === last.createTime
}
