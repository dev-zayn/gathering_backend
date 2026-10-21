import { clsx } from 'clsx'
import { twMerge } from 'tailwind-merge'

// Join class names, letting a later one win over an earlier one on the same
// property. Without the merge the winner is whichever Tailwind emitted first.
export function cn(...inputs) {
  return twMerge(clsx(inputs))
}
