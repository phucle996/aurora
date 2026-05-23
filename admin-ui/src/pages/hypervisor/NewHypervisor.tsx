import { useEffect } from 'react'
import { useNavigate } from '@tanstack/react-router'

export default function NewHypervisorPage() {
  const navigate = useNavigate()

  useEffect(() => {
    void navigate({ to: '/hypervisor', replace: true })
  }, [navigate])

  return null
}
