import { Routes, Route, Navigate } from 'react-router-dom'
import { useState } from 'react'
import Sidebar from './components/Sidebar.jsx'
import Header from './components/Header.jsx'
import Home from './pages/Home.jsx'
import AuthTokenModel from './pages/auth/TokenModel.jsx'
import ZoneManagement from './pages/zone/Management.jsx'
import ZoneWorkflow from './pages/zone/Workflow.jsx'
import SecurityRateLimit from './pages/security/RateLimit.jsx'
import SecuritySecrets from './pages/security/Secrets.jsx'
import ArchC1SystemContext from './pages/arch/C1SystemContext.jsx'
import ArchC2Container from './pages/arch/C2Container.jsx'
import ArchC3Component from './pages/arch/C3Component.jsx'
import ArchC4Code from './pages/arch/C4Code.jsx'
import { useTheme } from './lib/theme.js'

function PageWrapper({ children }) {
  return (
    <div className="max-w-[1760px] mx-auto px-6 py-5 space-y-12">
      {children}
    </div>
  )
}

function App() {
  const [sidebarOpen, setSidebarOpen] = useState(true)
  const theme = useTheme()

  return (
    <div className="flex h-screen bg-white dark:bg-slate-950 text-slate-900 dark:text-slate-100">
      <Sidebar open={sidebarOpen} />

      <div className="flex-1 flex flex-col overflow-hidden">
        <Header
          onMenuClick={() => setSidebarOpen(!sidebarOpen)}
          theme={theme.theme}
          onToggleTheme={theme.toggle}
        />
        <main className="flex-1 overflow-y-auto">
          <Routes>
            <Route path="/" element={<Navigate to="/auth/token-model" replace />} />
            <Route path="/auth/token-model" element={<PageWrapper><AuthTokenModel /></PageWrapper>} />
            <Route path="/zone/management" element={<PageWrapper><ZoneManagement /></PageWrapper>} />
            <Route path="/zone/workflow" element={<PageWrapper><ZoneWorkflow /></PageWrapper>} />
            <Route path="/security/rate-limit" element={<PageWrapper><SecurityRateLimit /></PageWrapper>} />
            <Route path="/security/secrets" element={<PageWrapper><SecuritySecrets /></PageWrapper>} />
            <Route path="/arch/c1" element={<PageWrapper><ArchC1SystemContext /></PageWrapper>} />
            <Route path="/arch/c2" element={<PageWrapper><ArchC2Container /></PageWrapper>} />
            <Route path="/arch/c3" element={<PageWrapper><ArchC3Component /></PageWrapper>} />
            <Route path="/arch/c4" element={<PageWrapper><ArchC4Code /></PageWrapper>} />
            <Route path="/home" element={<PageWrapper><Home /></PageWrapper>} />
            <Route path="*" element={<Navigate to="/auth/token-model" replace />} />
          </Routes>
        </main>
      </div>
    </div>
  )
}

export default App
