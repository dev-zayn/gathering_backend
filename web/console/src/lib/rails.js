import { Gamepad2, KeyRound } from 'lucide-react'

// A publisher's rail in the console's shell (docs/PUBLISHERS.md): its own
// games and its account. The admin's rail is the shell's default.
export const studioGroups = [
  [{ to: '/', label: 'My games', icon: Gamepad2, end: true }],
  [{ to: '/account', label: 'Account', icon: KeyRound }],
]
