import { useState, useEffect, useRef } from 'react'
import goLogo from '../assets/go.svg'
import rustLogo from '../assets/rust.svg'
import otelLogo from '../assets/otel.svg'
import prometheusLogo from '../assets/prometheus.svg'
import lokiLogo from '../assets/loki.svg'
import tempoLogo from '../assets/tempo.svg'
import grafanaLogo from '../assets/grafana.svg'

// ── Telemetry Flow Interactive Canvas Data ──────────────────────────────────────



const NODES = [
  {
    id: 'cp',
    x: 40, y: 60, w: 280, h: 100,
    title: 'Controlplane Cluster',
    subtitle: 'Go Application Telemetry Emitter',
    details: 'Exposes /metrics endpoint\nStructured JSON logging to stdout\nSends traces via OTLP/gRPC',
    color: { light: '#059669', dark: '#10b981', bg: 'rgba(16, 185, 129, 0.08)' }
  },
  {
    id: 'dp',
    x: 40, y: 300, w: 280, h: 100,
    title: 'Dataplane Zones',
    subtitle: 'Rust Node Telemetry Emitters',
    details: 'Rust edge daemon logs & metrics\nStructured JSON stdout logging\nSends traces via OTLP/gRPC',
    color: { light: '#7c3aed', dark: '#8b5cf6', bg: 'rgba(139, 92, 246, 0.08)' }
  },
  {
    id: 'otel',
    x: 460, y: 130, w: 280, h: 100,
    title: 'OpenTelemetry Collector',
    subtitle: 'Traces Processing Agent',
    details: 'Central trace aggregator (HA)\nOTLP gRPC/HTTP ingress ports\nLoad-balanced pushes to Tempo',
    color: { light: '#e11d48', dark: '#f43f5e', bg: 'rgba(244, 63, 94, 0.08)' }
  },
  {
    id: 'promtail',
    x: 460, y: 330, w: 280, h: 100,
    title: 'Promtail Log Agent',
    subtitle: 'Log Shipper & Scraper',
    details: 'DaemonSet agent scraping /var/log/pods\nParses structured JSON container logs\nPushes logs to Loki distributor',
    color: { light: '#d97706', dark: '#f59e0b', bg: 'rgba(245, 158, 11, 0.08)' }
  },
  {
    id: 'prometheus',
    x: 880, y: 40, w: 280, h: 100,
    title: 'Prometheus',
    subtitle: 'Metrics Engine',
    details: 'Scrapes /metrics via ServiceMonitor\nHA-paired TSDB local storage\nPromQL engine & Alertmanager',
    color: { light: '#10b981', dark: '#34d399', bg: 'rgba(52, 211, 153, 0.08)' }
  },
  {
    id: 'loki',
    x: 880, y: 200, w: 280, h: 100,
    title: 'Loki',
    subtitle: 'Log Aggregator',
    details: 'HA distributed log engine\nObject storage (S3/GCS) backend\nLogQL query evaluation',
    color: { light: '#d97706', dark: '#fbbf24', bg: 'rgba(251, 191, 36, 0.08)' }
  },
  {
    id: 'tempo',
    x: 880, y: 360, w: 280, h: 100,
    title: 'Tempo',
    subtitle: 'Trace Backend',
    details: 'HA distributed tracing backend\nTrace-to-logs (Loki) correlation\nParquet-based object storage',
    color: { light: '#e11d48', dark: '#fda4af', bg: 'rgba(253, 164, 175, 0.08)' }
  },
  {
    id: 'grafana',
    x: 740, y: 520, w: 420, h: 100,
    title: 'Grafana Dashboards',
    subtitle: 'Unified visualization portal',
    details: 'Unified visualization portal\nCorrelated metrics/logs/traces panels\nConfigured via GitOps (dashboards-as-code)',
    color: { light: '#4f46e5', dark: '#6366f1', bg: 'rgba(99, 102, 241, 0.08)' }
  },
  {
    id: 'sre',
    x: 40, y: 520, w: 280, h: 100,
    title: '👤 SRE Operator',
    subtitle: 'Monitors System Health',
    details: 'Accesses dashboards via ingress\nQueries Prometheus, Loki & Tempo\nPerforms runbook operations',
    color: { light: '#2563eb', dark: '#3b82f6', bg: 'rgba(59, 130, 246, 0.08)' }
  }
]




const PATHS = {
  metricsScrape: {
    color: { light: 'rgba(16, 185, 129, 0.25)', dark: 'rgba(52, 211, 153, 0.25)' },
    activeColor: { light: '#10b981', dark: '#34d399' },
    points: [
      { x: 880, y: 75 },
      { x: 320, y: 75 }
    ],
    label: 'HTTP GET /metrics (Pull)',
    labelOffset: { x: -280, y: -8 },
    labelColor: { light: '#059669', dark: '#34d399' }
  },
  tracesCP: {
    color: { light: 'rgba(225, 29, 72, 0.25)', dark: 'rgba(244, 63, 94, 0.25)' },
    activeColor: { light: '#e11d48', dark: '#f43f5e' },
    points: [
      { x: 320, y: 100 },
      { x: 390, y: 100 },
      { x: 390, y: 150 },
      { x: 460, y: 150 }
    ],
    label: 'Traces (OTLP)',
    labelOffset: { x: 5, y: -6 }
  },
  tracesDP: {
    color: { light: 'rgba(225, 29, 72, 0.25)', dark: 'rgba(244, 63, 94, 0.25)' },
    activeColor: { light: '#e11d48', dark: '#f43f5e' },
    points: [
      { x: 320, y: 320 },
      { x: 390, y: 320 },
      { x: 390, y: 170 },
      { x: 460, y: 170 }
    ]
  },
  logsCP: {
    color: { light: 'rgba(217, 119, 6, 0.25)', dark: 'rgba(245, 158, 11, 0.25)' },
    activeColor: { light: '#d97706', dark: '#f59e0b' },
    points: [
      { x: 320, y: 120 },
      { x: 410, y: 120 },
      { x: 410, y: 350 },
      { x: 460, y: 350 }
    ],
    label: 'Logs (Stdout)',
    labelOffset: { x: 5, y: -6 }
  },
  logsDP: {
    color: { light: 'rgba(217, 119, 6, 0.25)', dark: 'rgba(245, 158, 11, 0.25)' },
    activeColor: { light: '#d97706', dark: '#f59e0b' },
    points: [
      { x: 320, y: 360 },
      { x: 460, y: 360 }
    ]
  },
  otelToTempo: {
    color: { light: 'rgba(225, 29, 72, 0.25)', dark: 'rgba(244, 63, 94, 0.25)' },
    activeColor: { light: '#e11d48', dark: '#f43f5e' },
    points: [
      { x: 740, y: 180 },
      { x: 810, y: 180 },
      { x: 810, y: 410 },
      { x: 880, y: 410 }
    ],
    label: 'Push Traces',
    labelOffset: { x: 8, y: 25 }
  },
  promtailToLoki: {
    color: { light: 'rgba(217, 119, 6, 0.25)', dark: 'rgba(245, 158, 11, 0.25)' },
    activeColor: { light: '#d97706', dark: '#f59e0b' },
    points: [
      { x: 740, y: 380 },
      { x: 830, y: 380 },
      { x: 830, y: 250 },
      { x: 880, y: 250 }
    ],
    label: 'Push Logs',
    labelOffset: { x: 8, y: -80 }
  },
  promToGrafana: {
    color: { light: 'rgba(148, 163, 184, 0.25)', dark: 'rgba(71, 85, 105, 0.25)' },
    activeColor: { light: '#6366f1', dark: '#818cf8' },
    points: [
      { x: 1020, y: 140 },
      { x: 1020, y: 520 }
    ]
  },
  lokiToGrafana: {
    color: { light: 'rgba(148, 163, 184, 0.25)', dark: 'rgba(71, 85, 105, 0.25)' },
    activeColor: { light: '#6366f1', dark: '#818cf8' },
    points: [
      { x: 990, y: 300 },
      { x: 990, y: 520 }
    ]
  },
  tempoToGrafana: {
    color: { light: 'rgba(148, 163, 184, 0.25)', dark: 'rgba(71, 85, 105, 0.25)' },
    activeColor: { light: '#6366f1', dark: '#818cf8' },
    points: [
      { x: 1050, y: 460 },
      { x: 1050, y: 520 }
    ]
  },
  sreToGrafana: {
    color: { light: 'rgba(99, 102, 241, 0.25)', dark: 'rgba(99, 102, 241, 0.25)' },
    activeColor: { light: '#4f46e5', dark: '#6366f1' },
    points: [
      { x: 320, y: 570 },
      { x: 740, y: 570 }
    ],
    label: 'Query Dashboards',
    labelOffset: { x: 20, y: -8 },
    labelColor: { light: '#4f46e5', dark: '#818cf8' }
  }
}

function drawRoundedPath(ctx, points, radius = 8) {
  if (points.length < 2) return
  ctx.moveTo(points[0].x, points[0].y)
  for (let i = 1; i < points.length - 1; i++) {
    const p1 = points[i]
    const p2 = points[i + 1]
    ctx.arcTo(p1.x, p1.y, p2.x, p2.y, radius)
  }
  ctx.lineTo(points[points.length - 1].x, points[points.length - 1].y)
}

function drawArrowhead(ctx, p1, p2, size = 6) {
  const dx = p2.x - p1.x
  const dy = p2.y - p1.y
  const angle = Math.atan2(dy, dx)
  
  ctx.beginPath()
  ctx.moveTo(p2.x, p2.y)
  ctx.lineTo(p2.x - size * Math.cos(angle - Math.PI / 6), p2.y - size * Math.sin(angle - Math.PI / 6))
  ctx.lineTo(p2.x - size * Math.cos(angle + Math.PI / 6), p2.y - size * Math.sin(angle + Math.PI / 6))
  ctx.closePath()
  ctx.fill()
}

function getPositionAlongPath(points, progress) {
  if (points.length < 2) return points[0] || { x: 0, y: 0 }
  let totalLength = 0
  const lengths = []
  for (let i = 0; i < points.length - 1; i++) {
    const dx = points[i + 1].x - points[i].x
    const dy = points[i + 1].y - points[i].y
    const len = Math.sqrt(dx * dx + dy * dy)
    totalLength += len
    lengths.push(len)
  }
  let targetLen = progress * totalLength
  let accumulated = 0
  for (let i = 0; i < lengths.length; i++) {
    const len = lengths[i]
    if (accumulated + len >= targetLen) {
      const segProgress = (targetLen - accumulated) / len
      const p1 = points[i]
      const p2 = points[i + 1]
      return {
        x: p1.x + (p2.x - p1.x) * segProgress,
        y: p1.y + (p2.y - p1.y) * segProgress
      }
    }
    accumulated += len
  }
  return points[points.length - 1]
}

const ICON_URLS = {
  cp: goLogo,
  dp: rustLogo,
  otel: otelLogo,
  promtail: lokiLogo,
  prometheus: prometheusLogo,
  loki: lokiLogo,
  tempo: tempoLogo,
  grafana: grafanaLogo
}

export default function TelemetryCanvas() {
  const canvasRef = useRef(null)
  const [hoveredNode, setHoveredNode] = useState(null)
  const particlesRef = useRef([])
  const animFrameIdRef = useRef(null)
  const frameCountRef = useRef(0)
  const imagesRef = useRef({})
  const iconCacheRef = useRef({})

  // Asynchronously load and cache brand logos
  useEffect(() => {
    Object.keys(ICON_URLS).forEach(key => {
      const img = new Image()
      img.crossOrigin = 'anonymous'
      img.src = ICON_URLS[key]
      img.onload = () => {
        imagesRef.current[key] = img
      }
    })
  }, [])

  const isPathActive = (pathId) => {
    if (!hoveredNode) return false
    return (
      (hoveredNode.id === 'cp' && (pathId === 'metricsScrape' || pathId === 'tracesCP' || pathId === 'logsCP')) ||
      (hoveredNode.id === 'dp' && (pathId === 'tracesDP' || pathId === 'logsDP')) ||
      (hoveredNode.id === 'otel' && (pathId === 'tracesCP' || pathId === 'tracesDP' || pathId === 'otelToTempo')) ||
      (hoveredNode.id === 'promtail' && (pathId === 'logsCP' || pathId === 'logsDP' || pathId === 'promtailToLoki')) ||
      (hoveredNode.id === 'prometheus' && (pathId === 'metricsScrape' || pathId === 'promToGrafana')) ||
      (hoveredNode.id === 'loki' && (pathId === 'promtailToLoki' || pathId === 'lokiToGrafana')) ||
      (hoveredNode.id === 'tempo' && (pathId === 'otelToTempo' || pathId === 'tempoToGrafana')) ||
      (hoveredNode.id === 'grafana' && (pathId === 'promToGrafana' || pathId === 'lokiToGrafana' || pathId === 'tempoToGrafana' || pathId === 'sreToGrafana')) ||
      (hoveredNode.id === 'sre' && pathId === 'sreToGrafana')
    )
  }

  const getPathIdForParticle = (pId) => {
    if (pId === 'metricsPullRequest' || pId === 'metricsResponse') return 'metricsScrape'
    if (pId === 'sreQuery') return 'sreToGrafana'
    if (pId === 'queryProm' || pId === 'responseProm') return 'promToGrafana'
    if (pId === 'queryLoki' || pId === 'responseLoki') return 'lokiToGrafana'
    if (pId === 'queryTempo' || pId === 'responseTempo') return 'tempoToGrafana'
    return pId
  }

  // Retrieve or create colored icons from cache for 60fps zero-garbage-collection performance
  const getCachedIcon = (node, isDark) => {
    const dpr = window.devicePixelRatio || 1
    const theme = isDark ? 'dark' : 'light'
    const cacheKey = `${node.id}-${theme}-${dpr}`
    if (iconCacheRef.current[cacheKey]) {
      return iconCacheRef.current[cacheKey]
    }

    const img = imagesRef.current[node.id]
    if (!img) return null

    // Support Retina scaling for the cached canvas itself to maintain high-DPI crispness
    const logicalSize = 32
    const physicalSize = logicalSize * dpr

    const canvas = document.createElement('canvas')
    canvas.width = physicalSize
    canvas.height = physicalSize
    const ctx = canvas.getContext('2d')
    ctx.scale(dpr, dpr)
    ctx.drawImage(img, 0, 0, logicalSize, logicalSize)

    // Only apply color mask for Rust to ensure visibility/readability (white in dark mode, black in light mode)
    if (node.id === 'dp') {
      ctx.globalCompositeOperation = 'source-in'
      ctx.fillStyle = isDark ? '#ffffff' : '#000000'
      ctx.fillRect(0, 0, logicalSize, logicalSize)
    }

    iconCacheRef.current[cacheKey] = canvas
    return canvas
  }

  const handleMouseMove = (e) => {
    const canvas = canvasRef.current
    if (!canvas) return
    const rect = canvas.getBoundingClientRect()
    const x = (e.clientX - rect.left) * (1200 / rect.width)
    const y = (e.clientY - rect.top) * (660 / rect.height)
    let found = null
    for (const node of NODES) {
      if (x >= node.x && x <= node.x + node.w && y >= node.y && y <= node.y + node.h) {
        found = node
        break
      }
    }
    setHoveredNode(found)
  }

  const handleMouseLeave = () => {
    setHoveredNode(null)
  }

  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return
    const ctx = canvas.getContext('2d')

    // High-DPI physical-to-logical pixel scaling to guarantee crystal-clear lines & fonts
    const resize = () => {
      const dpr = window.devicePixelRatio || 1
      const rect = canvas.getBoundingClientRect()
      const width = rect.width || 1200
      const height = rect.height || 660

      canvas.width = width * dpr
      canvas.height = height * dpr

      const scaleX = (width / 1200) * dpr
      const scaleY = (height / 660) * dpr
      ctx.scale(scaleX, scaleY)
    }
    resize()
    window.addEventListener('resize', resize)

    const render = () => {
      frameCountRef.current += 1
      const frame = frameCountRef.current
      const isDark = document.documentElement.classList.contains('dark')
      ctx.clearRect(0, 0, 1200, 660)

      // Draw grid
      ctx.beginPath()
      ctx.strokeStyle = isDark ? 'rgba(99, 102, 241, 0.04)' : 'rgba(0, 0, 0, 0.02)'
      ctx.lineWidth = 1
      for (let x = 0; x < 1200; x += 20) {
        ctx.moveTo(x, 0)
        ctx.lineTo(x, 660)
      }
      for (let y = 0; y < 660; y += 20) {
        ctx.moveTo(0, y)
        ctx.lineTo(1200, y)
      }
      ctx.stroke()

      // Draw paths (all always visible, but active paths connected to hoveredNode are highlighted)
      Object.keys(PATHS).forEach(pathId => {
        const path = PATHS[pathId]
        const isHoveredPath = isPathActive(pathId)

        ctx.beginPath()
        drawRoundedPath(ctx, path.points, 10)
        ctx.strokeStyle = isHoveredPath
          ? (isDark ? path.activeColor.dark : path.activeColor.light)
          : (isDark ? path.color.dark : path.color.light)
        ctx.lineWidth = isHoveredPath ? 2.5 : 1.2
        if (pathId === 'metricsScrape') {
          ctx.setLineDash([4, 4])
        } else {
          ctx.setLineDash([])
        }
        ctx.stroke()
        ctx.setLineDash([])

        // Draw directional request arrowhead at the end of the path
        if (path.points.length >= 2) {
          const p1 = path.points[path.points.length - 2]
          const p2 = path.points[path.points.length - 1]
          ctx.fillStyle = isHoveredPath
            ? (isDark ? path.activeColor.dark : path.activeColor.light)
            : (isDark ? path.color.dark : path.color.light)
          drawArrowhead(ctx, p1, p2, 6)
        }

        // Only draw path label when active to prevent clutter
        if (path.label && isHoveredPath) {
          const firstPt = path.points[0]
          const lx = firstPt.x + path.labelOffset.x
          const ly = firstPt.y + path.labelOffset.y
          ctx.font = 'bold 9px sans-serif'
          ctx.fillStyle = path.labelColor
            ? (isDark ? path.labelColor.dark : path.labelColor.light)
            : (isDark ? '#64748b' : '#64748b')
          ctx.fillText(path.label, lx, ly)
        }
      })

      // Update & Draw Particles (only for active paths)
      const nextParticles = []
      particlesRef.current.forEach(p => {
        p.progress += p.speed
        if (p.progress < 1) {
          nextParticles.push(p)

          const pathId = getPathIdForParticle(p.id)
          if (!isPathActive(pathId)) return // Skip drawing particles for inactive paths

          const trailLength = 6
          for (let j = 0; j < trailLength; j++) {
            const trailProgress = Math.max(0, p.progress - (j * 0.015))
            const pos = getPositionAlongPath(p.points, trailProgress)
            ctx.beginPath()
            ctx.arc(pos.x, pos.y, p.size * (1 - j / trailLength), 0, Math.PI * 2)
            ctx.fillStyle = p.color
            ctx.globalAlpha = p.alpha * (1 - j / trailLength) * 0.8
            ctx.fill()
          }
          ctx.globalAlpha = 1.0
        } else {
          // Chain to next nodes in the telemetry pipeline
          if (p.id === 'tracesCP' || p.id === 'tracesDP') {
            if (isPathActive('otelToTempo')) {
              nextParticles.push({
                id: 'otelToTempo',
                points: PATHS.otelToTempo.points,
                color: isDark ? '#fda4af' : '#e11d48',
                size: 3.5,
                speed: 0.015,
                progress: 0,
                alpha: 1
              })
            }
          } else if (p.id === 'logsCP' || p.id === 'logsDP') {
            if (isPathActive('promtailToLoki')) {
              nextParticles.push({
                id: 'promtailToLoki',
                points: PATHS.promtailToLoki.points,
                color: isDark ? '#fbbf24' : '#d97706',
                size: 3.5,
                speed: 0.012,
                progress: 0,
                alpha: 1
              })
            }
          } else if (p.id === 'metricsPullRequest') {
            if (isPathActive('metricsScrape')) {
              nextParticles.push({
                id: 'metricsResponse',
                points: [...PATHS.metricsScrape.points].reverse(),
                color: isDark ? '#34d399' : '#10b981',
                size: 4,
                speed: 0.018,
                progress: 0,
                alpha: 1
              })
            }
          } else if (p.id === 'sreQuery') {
            if (isPathActive('promToGrafana')) {
              nextParticles.push({
                id: 'queryProm',
                points: [...PATHS.promToGrafana.points].reverse(),
                color: isDark ? '#818cf8' : '#4f46e5',
                size: 3,
                speed: 0.025,
                progress: 0,
                alpha: 1
              })
            }
            if (isPathActive('lokiToGrafana')) {
              nextParticles.push({
                id: 'queryLoki',
                points: [...PATHS.lokiToGrafana.points].reverse(),
                color: isDark ? '#818cf8' : '#4f46e5',
                size: 3,
                speed: 0.025,
                progress: 0,
                alpha: 1
              })
            }
            if (isPathActive('tempoToGrafana')) {
              nextParticles.push({
                id: 'queryTempo',
                points: [...PATHS.tempoToGrafana.points].reverse(),
                color: isDark ? '#818cf8' : '#4f46e5',
                size: 3,
                speed: 0.025,
                progress: 0,
                alpha: 1
              })
            }
          } else if (p.id === 'queryProm') {
            if (isPathActive('promToGrafana')) {
              nextParticles.push({
                id: 'responseProm',
                points: PATHS.promToGrafana.points,
                color: isDark ? '#34d399' : '#10b981',
                size: 4,
                speed: 0.02,
                progress: 0,
                alpha: 1
              })
            }
          } else if (p.id === 'queryLoki') {
            if (isPathActive('lokiToGrafana')) {
              nextParticles.push({
                id: 'responseLoki',
                points: PATHS.lokiToGrafana.points,
                color: isDark ? '#fbbf24' : '#d97706',
                size: 4,
                speed: 0.018,
                progress: 0,
                alpha: 1
              })
            }
          } else if (p.id === 'queryTempo') {
            if (isPathActive('tempoToGrafana')) {
              nextParticles.push({
                id: 'responseTempo',
                points: PATHS.tempoToGrafana.points,
                color: isDark ? '#fda4af' : '#e11d48',
                size: 4,
                speed: 0.022,
                progress: 0,
                alpha: 1
              })
            }
          }
        }
      })
      particlesRef.current = nextParticles

      // Dynamically spawn new packets only for the active paths to optimize CPU overhead
      if (hoveredNode) {
        if (isPathActive('tracesCP') && frame % 70 === 0) {
          particlesRef.current.push({
            id: 'tracesCP',
            points: PATHS.tracesCP.points,
            color: isDark ? '#fda4af' : '#e11d48',
            size: 3.5,
            speed: 0.015,
            progress: 0,
            alpha: 1
          })
        }
        if (isPathActive('tracesDP') && (frame + 35) % 70 === 0) {
          particlesRef.current.push({
            id: 'tracesDP',
            points: PATHS.tracesDP.points,
            color: isDark ? '#fda4af' : '#e11d48',
            size: 3.5,
            speed: 0.015,
            progress: 0,
            alpha: 1
          })
        }
        if (isPathActive('logsCP') && frame % 50 === 0) {
          particlesRef.current.push({
            id: 'logsCP',
            points: PATHS.logsCP.points,
            color: isDark ? '#fbbf24' : '#d97706',
            size: 3.5,
            speed: 0.012,
            progress: 0,
            alpha: 1
          })
        }
        if (isPathActive('logsDP') && (frame + 25) % 50 === 0) {
          particlesRef.current.push({
            id: 'logsDP',
            points: PATHS.logsDP.points,
            color: isDark ? '#fbbf24' : '#d97706',
            size: 3.5,
            speed: 0.012,
            progress: 0,
            alpha: 1
          })
        }
        if (isPathActive('metricsScrape') && frame % 90 === 0) {
          particlesRef.current.push({
            id: 'metricsPullRequest',
            points: PATHS.metricsScrape.points,
            color: isDark ? '#64748b' : '#94a3b8',
            size: 2.5,
            speed: 0.025,
            progress: 0,
            alpha: 0.8
          })
        }
        if (isPathActive('sreToGrafana') && frame % 160 === 0) {
          particlesRef.current.push({
            id: 'sreQuery',
            points: PATHS.sreToGrafana.points,
            color: isDark ? '#818cf8' : '#4f46e5',
            size: 4,
            speed: 0.018,
            progress: 0,
            alpha: 1
          })
        }
      }

      // Draw Nodes
      NODES.forEach(node => {
        const isHovered = hoveredNode && hoveredNode.id === node.id
        ctx.save()
        ctx.beginPath()
        ctx.roundRect(node.x, node.y, node.w, node.h, 10)
        ctx.fillStyle = isDark ? '#0f172a' : '#ffffff'
        ctx.strokeStyle = isHovered
          ? (isDark ? node.color.dark : node.color.light)
          : (isDark ? '#1e293b' : '#cbd5e1')
        ctx.lineWidth = isHovered ? 2.5 : 1.2
        if (isHovered) {
          ctx.shadowBlur = 15
          ctx.shadowColor = (isDark ? node.color.dark : node.color.light) + '44'
        }
        ctx.fill()
        ctx.stroke()
        ctx.restore()

        // Sidebar strip
        ctx.beginPath()
        ctx.roundRect(node.x, node.y, 6, node.h, { topLeft: 10, bottomLeft: 10 })
        ctx.fillStyle = isDark ? node.color.dark : node.color.light
        ctx.fill()

        // Render cached high-resolution app icon on the left
        const cachedIconCanvas = getCachedIcon(node, isDark)
        const logicalSize = 32
        let textPadding = 18

        if (cachedIconCanvas) {
          const ix = node.x + 16
          const iy = node.y + (node.h - logicalSize) / 2
          ctx.drawImage(cachedIconCanvas, ix, iy, logicalSize, logicalSize)
          textPadding = 60
        }

        // Draw node title
        ctx.font = 'bold 12px sans-serif'
        ctx.fillStyle = isDark ? '#f8fafc' : '#0f172a'
        ctx.fillText(node.title, node.x + textPadding, node.y + 24)

        // Draw node subtitle
        ctx.font = '500 10px sans-serif'
        ctx.fillStyle = isDark ? '#94a3b8' : '#475569'
        ctx.fillText(node.subtitle, node.x + textPadding, node.y + 38)

        // Draw node technical details
        ctx.font = 'normal 9.5px monospace'
        ctx.fillStyle = isDark ? '#64748b' : '#64748b'
        const detailsLines = node.details.split('\n')
        detailsLines.forEach((line, idx) => {
          ctx.fillText(line, node.x + textPadding, node.y + 54 + (idx * 11))
        })
      })

      animFrameIdRef.current = requestAnimationFrame(render)
    }

    render()

    return () => {
      cancelAnimationFrame(animFrameIdRef.current)
      window.removeEventListener('resize', resize)
    }
  }, [hoveredNode])

  return (
    <div className="relative w-full rounded-2xl border border-slate-200 dark:border-slate-800 bg-slate-50/50 dark:bg-slate-950/20 p-4 md:p-6 overflow-x-auto">
      <canvas
        ref={canvasRef}
        className="w-full h-auto min-w-[1000px] lg:min-w-0 cursor-crosshair block"
        onMouseMove={handleMouseMove}
        onMouseLeave={handleMouseLeave}
        style={{ aspectRatio: '1200/660' }}
      />
    </div>
  )
}


