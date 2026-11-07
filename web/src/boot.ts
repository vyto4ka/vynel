// The language is loaded before the app's modules run: they read messages while they load
// (labels in module-level tables), so a change of language is a page reload.
import { detectLang, setLang } from './i18n'
import { applyTheme, loadTheme } from './theme'

// The theme is applied before the app renders: styles.css is full of --base-driven rules, and
// a paint in the wrong palette first would flash.
applyTheme(loadTheme())
setLang(detectLang()).then(() => import('./main'))
