import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MotionConfig } from 'motion/react'
import { App } from './App'
import { ToastProvider } from './components/Toast'
import { ApiError } from './lib/api'
import './styles.css'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: false,
      // Galat 4xx (mis. 404 untuk draf yang baru dihapus) tidak akan berubah bila diulang;
      // hanya galat jaringan dan 5xx yang dicoba lagi.
      retry: (count, err) => !(err instanceof ApiError && err.status < 500) && count < 3,
    },
  },
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      {/* Animasi otomatis dimatikan bila pengguna memilih "kurangi gerakan" di sistem operasinya. */}
      <MotionConfig reducedMotion="user">
        <BrowserRouter>
          <ToastProvider>
            <App />
          </ToastProvider>
        </BrowserRouter>
      </MotionConfig>
    </QueryClientProvider>
  </StrictMode>,
)
