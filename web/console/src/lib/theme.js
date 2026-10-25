// Light, dark, or whatever the system says: the rail's switch. boot.js reads
// the same key before React starts, so a reload paints the right colours.
const KEY = 'ga.console.theme'

export function savedTheme() {
  try {
    const v = localStorage.getItem(KEY)
    return v === 'light' || v === 'dark' ? v : 'system'
  } catch {
    return 'system'
  }
}

export function applyTheme(theme) {
  const root = document.documentElement
  if (theme === 'system') delete root.dataset.theme
  else root.dataset.theme = theme
  try {
    if (theme === 'system') localStorage.removeItem(KEY)
    else localStorage.setItem(KEY, theme)
  } catch {
    /* a private window: it lasts until the tab closes */
  }
}

export const nextTheme = { system: 'light', light: 'dark', dark: 'system' }
