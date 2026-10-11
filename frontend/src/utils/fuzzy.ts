// fuzzyScore performs a case-insensitive fuzzy match and returns a relevance
// score where lower is better:
//   - a contiguous substring match scores its start index (0 = prefix match);
//   - an in-order subsequence match scores above 1000, penalised by its span;
//   - no match (or a subsequence spread too far apart) returns Infinity.
// Subsequences must be dense: the matched characters may span less than 2x the
// query length. That keeps the filter useful — "test" matches "test_01" but
// not "ods_illegal_tyqresult" or "activeInfoResult", which only contain
// t-e-s-t scattered far apart.

// normalizedScore 是已归一化(query 已 trim+小写、text 已小写)的打分内核,
// 语义与 fuzzyScore 完全一致。抽出来是为了批量过滤(fuzzyFilter)时 query
// 只归一化一次,不必逐项重复 trim/toLowerCase。
function normalizedScore(q: string, t: string): number {
  if (q === '') return 0
  const sub = t.indexOf(q)
  if (sub >= 0) return sub
  let qi = 0
  let first = -1
  let last = -1
  for (let ti = 0; ti < t.length && qi < q.length; ti++) {
    if (t[ti] === q[qi]) {
      if (first === -1) first = ti
      last = ti
      qi++
    }
  }
  if (qi !== q.length) return Infinity
  if (last - first >= q.length * 2) return Infinity
  return 1000 + (last - first)
}

export function fuzzyScore(query: string, text: string): number {
  return normalizedScore(query.trim().toLowerCase(), text.toLowerCase())
}

// fuzzyMatch is the boolean form of fuzzyScore for callers that only need a
// filter. An empty/blank query matches anything.
export function fuzzyMatch(query: string, text: string): boolean {
  return fuzzyScore(query, text) !== Infinity
}

// fuzzyFilter 按模糊相关度过滤 list 并按相关度升序排序(打分语义与
// fuzzyScore 一致,平分时保持原相对顺序——sort 为稳定排序)。query 只
// trim/toLowerCase 一次;空(或全空白)查询原样返回传入的同一数组引用,
// 与树内各处「未过滤时直接返回原清单」的既有行为一致。
export function fuzzyFilter<T>(list: T[], query: string, keyOf: (item: T) => string): T[] {
  const q = query.trim().toLowerCase()
  if (q === '') return list
  const scored = list
    .map((item) => ({ item, score: normalizedScore(q, keyOf(item).toLowerCase()) }))
    .filter((x) => x.score !== Infinity)
  scored.sort((a, b) => a.score - b.score)
  return scored.map((x) => x.item)
}
