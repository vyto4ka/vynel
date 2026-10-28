// Kept apart from index.tsx: the catalogs import it while index.tsx imports the catalogs.
export type Plural = { _: 'plural'; other: string; zero?: string; one?: string; two?: string; few?: string; many?: string }

// p makes a message that depends on a number: t('key', { n: 3 }) picks the form by the rules of the language.
export const p = (forms: Omit<Plural, '_'>): Plural => ({ _: 'plural', ...forms })
