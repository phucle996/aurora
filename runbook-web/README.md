# 🚀 Runbook Web - Aurora Admin Authentication Documentation

Interactive web documentation for Aurora Admin Authentication Token Model & Lifecycle.

## 📋 Overview

A modern React + Vite + Tailwind CSS web application that visualizes and documents the Aurora Admin authentication system, including:

- **3-Fragment Token Architecture** (JWT + AccessKey + AccessSecret)
- **Token Lifecycle** (Login, Refresh, Logout, Inactivity)
- **Security Properties** (Device Binding, Replay Protection, HA Safe)
- **Configuration & Guarantees**

## 🛠️ Tech Stack

- **React 19.2.6** - UI framework
- **Vite 8.0.12** - Fast build tool
- **Tailwind CSS 3.4.17** - Utility-first CSS
- **Node.js 20** - Runtime
- **Docker** - Containerization

## 🎨 Features

✅ Dark theme with gradient UI  
✅ Responsive sidebar navigation  
✅ Collapsible menu on mobile  
✅ Fragment token visualization  
✅ Security properties grid  
✅ Smooth animations & transitions  
✅ Production-ready badge  

## 📁 Project Structure

```
runbook-web/
├── src/
│   ├── components/
│   │   ├── Sidebar.jsx      # Navigation sidebar
│   │   ├── Header.jsx       # Top header
│   │   └── Content.jsx      # Main content area
│   ├── App.jsx              # Main app component
│   ├── index.css            # Tailwind CSS
│   └── main.jsx
├── Dockerfile               # Docker image
├── package.json
├── vite.config.js
├── tailwind.config.js
└── postcss.config.js
```

## 🚀 Getting Started

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
cd /home/phucle/Desktop/New/runbook-web
npm run build
npm run preview
```

## 📝 Available Scripts

```bash
npm run dev      # Start dev server (Vite)
npm run build    # Build for production
npm run preview  # Preview production build
npm run lint     # Run ESLint
```

## 🐳 Docker Services

The `docker-compose.dev.yml` includes:

- **postgres:16-alpine** (Port 5432)
- **redis:7-alpine** (Port 6379)
- **controlplane** (Port 8080)
- **runbook-web** (Port 5173) ← This project

## 🎯 Customization

### Add New Sections

Edit `src/components/Sidebar.jsx` to add navigation items:

```jsx
const sections = [
  { id: 'new-section', label: 'New Section', icon: '📌' },
  // ...
]
```

### Update Content

Edit `src/components/Content.jsx` to add new sections:

```jsx
<section id="new-section">
  <h2 className="text-2xl font-bold mb-6">New Section</h2>
  {/* Your content here */}
</section>
```

### Customize Styling

Tailwind CSS is configured in `tailwind.config.js`. Modify colors, spacing, and other utilities as needed.

## 📚 Documentation

For Aurora Admin Authentication specification, see:
- `spec/admin_auth_security_specification.md` - Full specification
- `spec/fragment_token_visualization.html` - Interactive visualization

## 🔗 Related Projects

- **controlplane** - Backend API server
- **admin-ui** - Admin dashboard
- **spec/** - Security specifications

## 📄 License

Part of Aurora project.

## 🤝 Contributing

To add new content:

1. Update `src/components/Sidebar.jsx` with new section
2. Add corresponding section in `src/components/Content.jsx`
3. Test locally: `npm run dev`
4. Build: `npm run build`

## 📞 Support

For issues or questions about the Aurora Admin Authentication system, refer to the specification documents in the `spec/` directory.

---

**Version**: 2.2  
**Updated**: 2026-05-29  
**Status**: Production Ready
