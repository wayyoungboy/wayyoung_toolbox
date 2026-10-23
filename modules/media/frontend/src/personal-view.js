export function offsetAfterRemoval(offset, total, removed, pageSize = 48) {
  return Math.min(offset, Math.max(0, Math.ceil((total - removed) / pageSize) - 1) * pageSize);
}

export function moveListWork(ids, id, delta) {
  const result = [...ids], index = result.indexOf(id), next = index + delta;
  if (index < 0 || next < 0 || next >= result.length) return result;
  [result[index], result[next]] = [result[next], result[index]];
  return result;
}
