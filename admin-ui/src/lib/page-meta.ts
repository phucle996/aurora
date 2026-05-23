import { useEffect } from 'react'

function ensureMetaDescriptionTag(): HTMLMetaElement | null {
  if (typeof document === 'undefined') {
    return null
  }

  let tag = document.querySelector('meta[name="description"]') as HTMLMetaElement | null
  if (!tag) {
    tag = document.createElement('meta')
    tag.name = 'description'
    document.head.appendChild(tag)
  }

  return tag
}

function ensureMetaRobotsTag(): HTMLMetaElement | null {
  if (typeof document === 'undefined') {
    return null
  }

  let tag = document.querySelector('meta[name="robots"]') as HTMLMetaElement | null
  if (!tag) {
    tag = document.createElement('meta')
    tag.name = 'robots'
    document.head.appendChild(tag)
  }

  return tag
}

export function usePageMeta(title: string, description?: string): void {
  useEffect(() => {
    const previousTitle = document.title
    const descriptionTag = ensureMetaDescriptionTag()
    const robotsTag = ensureMetaRobotsTag()
    const previousDescription = descriptionTag?.content ?? ''
    const previousRobots = robotsTag?.content ?? ''

    document.title = title
    if (descriptionTag && description) {
      descriptionTag.content = description
    }
    if (robotsTag) {
      robotsTag.content = 'noindex,nofollow'
    }

    return () => {
      document.title = previousTitle
      if (descriptionTag) {
        descriptionTag.content = previousDescription
      }
      if (robotsTag) {
        robotsTag.content = previousRobots
      }
    }
  }, [title, description])
}
