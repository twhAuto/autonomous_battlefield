// Shared event page size and polling cadence used by battle hooks and the event console.


export const eventPageSize = 50

export const eventPollIntervalMs = 1000

// Upper bound for the retry delay after failed event polls on a live attempt.
export const eventRetryMaxMs = 30_000
