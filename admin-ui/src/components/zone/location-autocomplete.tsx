import { useEffect, useRef, useState } from 'react'
import { Loader2, MapPin } from 'lucide-react'

import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'

export type ZoneLocation = {
  label: string
  city: string
  country: string
  region: string
  lat: number
  lng: number
  suggestedCode: string
  custom?: boolean
}

const predefinedLocations: ZoneLocation[] = [
  { label: 'Ashburn, US', city: 'Ashburn', country: 'United States', region: 'North America', lat: 39.0438, lng: -77.4874, suggestedCode: 'us-east-1' },
  { label: 'Los Angeles, US', city: 'Los Angeles', country: 'United States', region: 'North America', lat: 34.0522, lng: -118.2437, suggestedCode: 'us-west-1' },
  { label: 'New York, US', city: 'New York', country: 'United States', region: 'North America', lat: 40.7128, lng: -74.006, suggestedCode: 'us-nyc-1' },
  { label: 'London, UK', city: 'London', country: 'United Kingdom', region: 'Europe', lat: 51.5072, lng: -0.1276, suggestedCode: 'uk-lon-1' },
  { label: 'Frankfurt, DE', city: 'Frankfurt', country: 'Germany', region: 'Europe', lat: 50.1109, lng: 8.6821, suggestedCode: 'eu-fra-1' },
  { label: 'Singapore', city: 'Singapore', country: 'Singapore', region: 'Asia Pacific', lat: 1.3521, lng: 103.8198, suggestedCode: 'sg-sin-1' },
  { label: 'Tokyo, JP', city: 'Tokyo', country: 'Japan', region: 'Asia Pacific', lat: 35.6762, lng: 139.6503, suggestedCode: 'jp-tok-1' },
  { label: 'Hong Kong', city: 'Hong Kong', country: 'Hong Kong', region: 'Asia Pacific', lat: 22.3193, lng: 114.1694, suggestedCode: 'hk-hkg-1' },
  { label: 'Sydney, AU', city: 'Sydney', country: 'Australia', region: 'Oceania', lat: -33.8688, lng: 151.2093, suggestedCode: 'au-syd-1' },
  { label: 'São Paulo, BR', city: 'São Paulo', country: 'Brazil', region: 'South America', lat: -23.5558, lng: -46.6396, suggestedCode: 'br-sao-1' },
]

type NominatimPlace = {
  place_id: number
  display_name: string
  lat: string
  lon: string
  address?: {
    city?: string
    town?: string
    village?: string
    county?: string
    state?: string
    country?: string
  }
}

const makeSuggestedCode = (place: Pick<ZoneLocation, 'city' | 'country'>) => {
  const countryCode = place.country
    .slice(0, 2)
    .toLowerCase()
    .replace(/[^a-z0-9]/g, '') || 'zz'
  const cityCode = place.city
    .toLowerCase()
    .normalize('NFD')
    .replace(/[̀-ͯ]/g, '')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-|-$/g, '')
    .slice(0, 12) || 'custom'

  return `${countryCode}-${cityCode}-1`
}

const mapPlaceToLocation = (place: NominatimPlace): ZoneLocation => {
  const city =
    place.address?.city ??
    place.address?.town ??
    place.address?.village ??
    place.address?.county ??
    place.display_name.split(',')[0]?.trim() ??
    'Pinned location'
  const country = place.address?.country ?? 'Unknown country'
  const region = place.address?.state ?? place.address?.county ?? country
  const base = { city, country, region, lat: Number(place.lat), lng: Number(place.lon), custom: true as const }

  return {
    ...base,
    label: place.display_name,
    suggestedCode: makeSuggestedCode(base),
  }
}

type LocationAutocompleteProps = {
  value: string
  onSelect: (location: ZoneLocation) => void
  className?: string
}

export function LocationAutocomplete({ value, onSelect, className }: LocationAutocompleteProps) {
  const [inputValue, setInputValue] = useState(value)
  const [searchResults, setSearchResults] = useState<ZoneLocation[]>([])
  const [isSearching, setIsSearching] = useState(false)
  const [open, setOpen] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)

  // sync external value changes (e.g. form reset)
  useEffect(() => {
    setInputValue(value)
  }, [value])

  // close dropdown on outside click
  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [])

  // Nominatim search with debounce
  useEffect(() => {
    const trimmed = inputValue.trim()
    if (trimmed.length < 2) {
      setSearchResults([])
      setIsSearching(false)
      return
    }

    const controller = new AbortController()
    const timeout = window.setTimeout(() => {
      setIsSearching(true)
      fetch(
        `https://nominatim.openstreetmap.org/search?format=jsonv2&addressdetails=1&limit=6&q=${encodeURIComponent(trimmed)}`,
        { signal: controller.signal },
      )
        .then((res) => (res.ok ? res.json() : []))
        .then((places: NominatimPlace[]) => {
          setSearchResults(places.map(mapPlaceToLocation))
        })
        .catch((err: Error) => {
          if (err.name !== 'AbortError') setSearchResults([])
        })
        .finally(() => {
          if (!controller.signal.aborted) setIsSearching(false)
        })
    }, 350)

    return () => {
      window.clearTimeout(timeout)
      controller.abort()
    }
  }, [inputValue])

  const handleSelect = (location: ZoneLocation) => {
    setInputValue(location.label)
    setSearchResults([])
    setOpen(false)
    onSelect(location)
  }

  const showPredefined = inputValue.trim().length < 2
  const items: ZoneLocation[] = showPredefined ? predefinedLocations : searchResults

  return (
    <div ref={containerRef} className={cn('relative mt-3', className)}>
      <div className="relative">
        <MapPin className="pointer-events-none absolute left-4 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          value={inputValue}
          onChange={(e) => {
            setInputValue(e.target.value)
            setOpen(true)
          }}
          onFocus={() => setOpen(true)}
          placeholder="Search location, e.g. Ho Chi Minh City"
          className="h-12 border-border bg-background pl-11 pr-10 shadow-none"
        />
        {isSearching && (
          <Loader2 className="pointer-events-none absolute right-4 top-1/2 size-4 -translate-y-1/2 animate-spin text-muted-foreground" />
        )}
      </div>

      {/* Dropdown */}
      {open && (
        <div className="absolute left-0 right-0 top-[calc(100%+6px)] z-50 overflow-hidden rounded border border-border bg-popover shadow-md">
          <p className="px-3 py-2 text-xs text-muted-foreground">
            {showPredefined ? 'Suggested Locations' : isSearching ? 'Searching...' : 'Search Results'}
          </p>

          {isSearching && (
            <div className="flex items-center gap-2 px-3 pb-3 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" />
              Searching...
            </div>
          )}

          {!isSearching && items.length === 0 && inputValue.trim().length >= 2 && (
            <p className="px-3 pb-3 text-sm text-muted-foreground">No locations found.</p>
          )}

          {!isSearching && items.map((loc, i) => (
            <button
              key={`${loc.suggestedCode}-${i}`}
              type="button"
              onMouseDown={(e) => {
                // prevent input blur before click fires
                e.preventDefault()
                handleSelect(loc)
              }}
              className="flex w-full flex-col gap-0.5 px-3 py-2 text-left transition-colors hover:bg-accent hover:text-accent-foreground"
            >
              <span className="text-sm font-medium">{loc.label}</span>
              <span className="text-xs text-muted-foreground">
                {loc.region} · {loc.suggestedCode}
              </span>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
