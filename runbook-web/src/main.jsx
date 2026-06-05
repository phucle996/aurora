import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import './index.css'
import './i18n/index.js'
import App from './App.jsx'
import { bootstrapTheme } from './lib/theme.js'
import { QuickNavProvider } from './context/QuickNavContext.jsx'

bootstrapTheme()

createRoot(document.getElementById('root')).render(
  <StrictMode>
    <BrowserRouter>
      <QuickNavProvider>
        <App />
      </QuickNavProvider>
    </BrowserRouter>
  </StrictMode>,
)

