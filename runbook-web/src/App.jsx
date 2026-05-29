import { useState } from 'react'
import Sidebar from './components/Sidebar.jsx'
import Header from './components/Header.jsx'
import Home from './pages/Home.jsx'
import AuthTokenModel from './pages/AuthTokenModel.jsx'
import { useTheme } from './lib/theme.js'

function App() {
  const [sidebarOpen, setSidebarOpen] = useState(true)
  const [currentPage, setCurrentPage] = useState('auth-token-model')
  const theme = useTheme()

  const renderPage = () => {
    switch (currentPage) {
      case 'auth-token-model':
        return <AuthTokenModel />
      case 'home':
      default:
        return <Home />
    }
  }

  return (
    <div className="flex h-screen bg-white dark:bg-slate-950 text-slate-900 dark:text-slate-100">
      <Sidebar
        open={sidebarOpen}
        currentPage={currentPage}
        onNavigate={setCurrentPage}
      />

      <div className="flex-1 flex flex-col overflow-hidden">
        <Header
          onMenuClick={() => setSidebarOpen(!sidebarOpen)}
          currentPage={currentPage}
          onNavigate={setCurrentPage}
          theme={theme.theme}
          onToggleTheme={theme.toggle}
        />
        <main className="flex-1 overflow-y-auto">
          {renderPage()}
        </main>
      </div>
    </div>
  )
}

export default App
