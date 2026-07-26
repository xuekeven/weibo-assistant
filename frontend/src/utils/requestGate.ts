export type RequestToken = {
  generation: number
  key: string
}

export function createLatestRequestGate() {
  let generation = 0

  return {
    begin(key: string): RequestToken {
      generation += 1
      return { generation, key }
    },
    isCurrent(token: RequestToken, key: string) {
      return token.generation === generation && token.key === key
    },
    invalidate() {
      generation += 1
    }
  }
}
