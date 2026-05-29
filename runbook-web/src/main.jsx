import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import './i18n/index.js'
import App from './App.jsx'
import { bootstrapTheme } from './lib/theme.js'

bootstrapTheme()

createRoot(document.getElementById('root')).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
