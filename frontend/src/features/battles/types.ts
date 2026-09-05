export type ConnectionState = 'connecting' | 'live' | 'reconnecting' | 'settled' | 'error'

export type EventState = 'idle' | 'loading' | 'error'

export type BattleControlState = 'idle' | 'pausing' | 'resuming' | 'stopping' | 'error'

export type BattleControlAction = 'pause' | 'resume' | 'stop'
