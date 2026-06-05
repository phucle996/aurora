import { useEffect, useRef, useState } from 'react'
import c4L1Img from '../assets/c4-l1.png'

export default function TopologyCanvas() {
  const canvasRef = useRef(null)
  const containerRef = useRef(null)
  const [image, setImage] = useState(null)

  // Load image once on mount with race condition safety
  useEffect(() => {
    let active = true
    const img = new Image()
    img.src = c4L1Img
    img.onload = () => {
      if (active) {
        setImage(img)
      }
    }
    return () => {
      active = false
    }
  }, [])

  // Draw image on canvas and handle resizing
  useEffect(() => {
    if (!image || !canvasRef.current || !containerRef.current) return

    const canvas = canvasRef.current
    const ctx = canvas.getContext('2d')
    const container = containerRef.current

    const handleResize = () => {
      const rect = container.getBoundingClientRect()
      const width = rect.width

      // Calculate layout height based on aspect ratio
      const aspectRatio = image.naturalHeight / image.naturalWidth
      const height = width * aspectRatio

      const dpr = window.devicePixelRatio || 1
      canvas.width = width * dpr
      canvas.height = height * dpr

      ctx.scale(dpr, dpr)
      ctx.clearRect(0, 0, width, height)
      ctx.drawImage(image, 0, 0, width, height)
    }

    // Run initial size calculation
    handleResize()

    // Observe size changes of the container
    const observer = new ResizeObserver(() => {
      handleResize()
    })
    observer.observe(container)

    return () => {
      observer.disconnect()
    }
  }, [image])

  return (
    <div
      ref={containerRef}
      className="relative w-full rounded-2xl border border-slate-200 dark:border-slate-800 bg-slate-50/50 dark:bg-slate-950/20 p-4 md:p-6 overflow-hidden"
    >
      <canvas
        ref={canvasRef}
        className="w-full h-auto block cursor-default"
      />
    </div>
  )
}
