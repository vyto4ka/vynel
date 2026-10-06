import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

function App() {
  return <main style={{ fontFamily: 'system-ui', padding: 24 }}>Панель в разработке (этап 7).</main>
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
