// Panel theme. The colors are tokens in styles.css: «miku» is the default set on :root, the
// others are Catppuccin palettes (Mocha, Macchiato, Frappé, Latte) applied via data-theme.
// The choice is client-side only, like the interface mode and language.
export type Theme = 'miku' | 'mocha' | 'macchiato' | 'frappe' | 'latte'

export const themes: { id: Theme; name: string; base: string }[] = [
  { id: 'miku', name: 'Miku', base: '#172226' },
  { id: 'mocha', name: 'Mocha', base: '#1e1e2e' },
  { id: 'macchiato', name: 'Macchiato', base: '#24273a' },
  { id: 'frappe', name: 'Frappé', base: '#303446' },
  { id: 'latte', name: 'Latte', base: '#eff1f5' },
]

const key = 'vynel.theme'

export function loadTheme(): Theme {
  try {
    const saved = localStorage.getItem(key)
    return themes.some((x) => x.id === saved) ? (saved as Theme) : 'miku'
  } catch {
    return 'miku'
  }
}

// applyTheme sets the palette on <html> and keeps meta theme-color in sync with the theme's base,
// so the browser UI (tab, mobile address bar) follows the theme without a reload.
export function applyTheme(theme: Theme) {
  document.documentElement.dataset.theme = theme
  try { localStorage.setItem(key, theme) } catch { /* приватный режим */ }
  const base = themes.find((x) => x.id === theme)?.base
  if (base) document.querySelector('meta[name="theme-color"]')?.setAttribute('content', base)
}
