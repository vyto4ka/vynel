import { useEffect, useLayoutEffect } from 'react'
import { t } from './i18n'

let dirtyPage = false

// The shell checks before unmounting the current editor on hash navigation/logout.
export function confirmPageLeave(): boolean {
  return !dirtyPage || window.confirm(t('subscription.leave_unsaved'))
}

export function useUnsavedChanges(dirty: boolean) {
  useLayoutEffect(() => {
    dirtyPage = dirty
    return () => { dirtyPage = false }
  }, [dirty])
  useEffect(() => {
    if (!dirty) return
    const beforeUnload = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = '' }
    window.addEventListener('beforeunload', beforeUnload)
    return () => window.removeEventListener('beforeunload', beforeUnload)
  }, [dirty])
}
