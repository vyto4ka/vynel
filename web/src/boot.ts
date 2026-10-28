// The language is loaded before the app's modules run: they read messages while they load
// (labels in module-level tables), so a change of language is a page reload.
import { detectLang, setLang } from './i18n'

setLang(detectLang()).then(() => import('./main'))
