# 🚀 Runbook Web - Setup Guide

## Quick Start

### Option 1: Docker Compose (Recommended)
```bash
cd /home/phucle/Desktop/New/controlplane
docker-compose -f docker-compose.dev.yml up
```
Then open: http://localhost:5173

### Option 2: Local Development
```bash
cd /home/phucle/Desktop/New/runbook-web
npm install
npm run dev
```
Then open: http://localhost:5173

### Option 3: Production Build
```bash
npm run build
npm run preview
```

## Available Commands

```bash
npm run dev      # Start dev server
npm run build    # Build for production
npm run preview  # Preview production build
npm run lint     # Run ESLint
```

## Project Structure

```
src/
├── components/
│   ├── Sidebar.jsx    # Navigation (13 sections)
│   ├── Header.jsx     # Top bar with menu
│   └── Content.jsx    # Main content
├── App.jsx            # Main component
├── index.css          # Tailwind CSS
└── main.jsx
```

## Customization

### Add Navigation Items
Edit `src/components/Sidebar.jsx`:
```jsx
const sections = [
  { id: 'new-section', label: 'New Section', icon: '📌' },
]
```

### Update Content
Edit `src/components/Content.jsx`:
```jsx
<section id="new-section">
  <h2>New Section</h2>
  {/* Your content */}
</section>
```

## Tech Stack

- React 19.2.6
- Vite 8.0.12
- Tailwind CSS 3.4.17
- Node.js 20 (Alpine)
- Docker

## Features

✅ Dark theme with gradients
✅ Responsive sidebar
✅ Fragment token visualization
✅ Security properties grid
✅ Mobile-friendly layout
✅ Production Ready

---

**Version**: 2.2  
**Status**: Production Ready  
**Port**: 5173
