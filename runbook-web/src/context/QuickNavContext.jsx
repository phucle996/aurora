import { createContext, useContext, useState } from 'react'

const QuickNavContext = createContext({
  navItems: [],
  setNavItems: () => {}
})

export function QuickNavProvider({ children }) {
  const [navItems, setNavItems] = useState([])

  return (
    <QuickNavContext.Provider value={{ navItems, setNavItems }}>
      {children}
    </QuickNavContext.Provider>
  )
}

export function useQuickNav() {
  return useContext(QuickNavContext)
}
