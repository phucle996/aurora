import { useEffect, useMemo, useState } from 'react'
import { Loader2, MapPin, Search } from 'lucide-react'
import { MapContainer, TileLayer, Marker, Popup, useMap, useMapEvents } from 'react-leaflet'
import 'leaflet/dist/leaflet.css'
import L from 'leaflet'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'

// eslint-disable-next-line @typescript-eslint/no-explicit-any
delete (L.Icon.Default.prototype as any)._getIconUrl
L.Icon.Default.mergeOptions({
  iconRetinaUrl: 'https://cdnjs.cloudflare.com/ajax/libs/leaflet/1.7.1/images/marker-icon-2x.png',
  iconUrl: 'https://cdnjs.cloudflare.com/ajax/libs/leaflet/1.7.1/images/marker-icon.png',
  shadowUrl: 'https://cdnjs.cloudflare.com/ajax/libs/leaflet/1.7.1/images/marker-shadow.png',
})

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

type SearchResult = ZoneLocation & {
  placeId: string
}

type NominatimPlace = {
  place_id: number
  display_name: string
  lat: string
  lon: string
  address?: {
    city?: string
    town?: string
    village?: string
    suburb?: string
    county?: string
    state?: string
    country?: string
    country_code?: string
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
    .replace(/[\u0300-\u036f]/g, '')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-|-$/g, '')
    .slice(0, 12) || 'custom'

  return `${countryCode}-${cityCode}-1`
}

const mapPlaceToLocation = (place: NominatimPlace): SearchResult => {
  const city = place.address?.city ?? place.address?.town ?? place.address?.village ?? place.address?.county ?? place.display_name.split(',')[0]?.trim() ?? 'Pinned location'
  const country = place.address?.country ?? 'Unknown country'
  const region = place.address?.state ?? place.address?.county ?? country
  const location = {
    placeId: String(place.place_id),
    label: place.display_name,
    city,
    country,
    region,
    lat: Number(place.lat),
    lng: Number(place.lon),
    suggestedCode: '',
    custom: true,
  }

  return {
    ...location,
    suggestedCode: makeSuggestedCode(location),
  }
}

function MapClickHandler({ onMapClick }: { onMapClick: (lat: number, lng: number) => void }) {
  useMapEvents({
    click(e) {
      onMapClick(e.latlng.lat, e.latlng.lng)
    },
  })
  return null
}

function MapUpdater({ location }: { location: ZoneLocation | null }) {
  const map = useMap()
  useEffect(() => {
    if (location) {
      map.flyTo([location.lat, location.lng], map.getZoom() < 4 ? 4 : map.getZoom(), {
        duration: 0.8
      })
    }
  }, [location, map])
  return null
}

type LocationPickerDialogProps = {
  open: boolean
  value: string
  onOpenChange: (open: boolean) => void
  onSelect: (location: ZoneLocation) => void
}

export function LocationPickerDialog({ open, value, onOpenChange, onSelect }: LocationPickerDialogProps) {
  const [query, setQuery] = useState('')
  const [customLoc, setCustomLoc] = useState<ZoneLocation | null>(null)
  const [searchResults, setSearchResults] = useState<SearchResult[]>([])
  const [isSearching, setIsSearching] = useState(false)
  const [isResolvingMapClick, setIsResolvingMapClick] = useState(false)
  
  const initialMatch = predefinedLocations.find((loc) => loc.label === value)
  const [activeLocation, setActiveLocation] = useState<ZoneLocation | null>(
    initialMatch ?? null
  )

  const mapLocations = useMemo(() => {
    const normalizedQuery = query.trim().toLowerCase()
    const baseList = customLoc ? [customLoc, ...predefinedLocations] : predefinedLocations
    
    if (!normalizedQuery) return baseList

    return baseList.filter((location) =>
      [location.label, location.city, location.country, location.region, location.suggestedCode].some((item) =>
        item.toLowerCase().includes(normalizedQuery),
      ),
    )
  }, [query, customLoc])

  useEffect(() => {
    const normalizedQuery = query.trim()
    if (!open || normalizedQuery.length < 2) {
      return
    }

    const controller = new AbortController()
    const timeout = window.setTimeout(() => {
      setIsSearching(true)
      fetch(
        `https://nominatim.openstreetmap.org/search?format=jsonv2&addressdetails=1&limit=6&q=${encodeURIComponent(normalizedQuery)}`,
        { signal: controller.signal },
      )
        .then((response) => (response.ok ? response.json() : []))
        .then((places: NominatimPlace[]) => {
          setSearchResults(places.map(mapPlaceToLocation))
        })
        .catch((error: Error) => {
          if (error.name !== 'AbortError') setSearchResults([])
        })
        .finally(() => {
          if (!controller.signal.aborted) setIsSearching(false)
        })
    }, 350)

    return () => {
      window.clearTimeout(timeout)
      controller.abort()
    }
  }, [open, query])

  const selected = activeLocation ?? null
  const showSearchPanel = open && query.trim().length >= 2 && (isSearching || searchResults.length > 0)

  const pickLocation = (location: ZoneLocation) => {
    setActiveLocation(location)
  }

  const pickSearchResult = (location: SearchResult) => {
    setCustomLoc(location)
    setActiveLocation(location)
    setQuery(location.label)
    setSearchResults([])
  }

  const handleMapClick = async (lat: number, lng: number) => {
    setIsResolvingMapClick(true)
    setSearchResults([])

    try {
      const response = await fetch(
        `https://nominatim.openstreetmap.org/reverse?format=jsonv2&addressdetails=1&lat=${lat}&lon=${lng}`,
      )
      const place = response.ok ? await response.json() as NominatimPlace : null
      const resolvedLocation = place?.display_name
        ? mapPlaceToLocation({ ...place, lat: String(lat), lon: String(lng) })
        : null
      const fallbackCity = `${lat.toFixed(4)}, ${lng.toFixed(4)}`
      const newLocation: ZoneLocation = resolvedLocation ?? {
        label: `Pinned location near ${fallbackCity}`,
        city: fallbackCity,
        country: 'Unknown country',
        region: 'Unknown region',
        lat,
        lng,
        suggestedCode: makeSuggestedCode({ city: fallbackCity, country: 'Unknown country' }),
        custom: true,
      }

      setCustomLoc(newLocation)
      setActiveLocation(newLocation)
      setQuery(resolvedLocation?.label ?? '')
    } finally {
      setIsResolvingMapClick(false)
    }
  }

  const confirmLocation = () => {
    if (!selected) return
    onSelect(selected)
    onOpenChange(false)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[1040px] w-[95vw] max-h-[95vh] gap-0 overflow-hidden p-0" showCloseButton>
        <div className="flex h-full max-h-[95vh] flex-col lg:h-[660px]">
          
          <div className="relative flex-1 flex flex-col bg-[radial-gradient(circle_at_top_left,rgba(37,99,235,0.14),transparent_34%),linear-gradient(135deg,#f8fbff_0%,#eef5ff_100%)] p-5 md:p-8">
            <DialogHeader className="relative z-10 max-w-2xl text-left shrink-0">
              <DialogTitle className="text-2xl font-semibold tracking-[-0.03em] text-foreground">
                Select zone location
              </DialogTitle>
              <DialogDescription>
                Search or click a point on the interactive map to choose where this infrastructure zone will live.
              </DialogDescription>
            </DialogHeader>

            <div className="relative z-20 mt-5 max-w-xl shrink-0">
              <Search className="pointer-events-none absolute left-4 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder="Search places like Google Maps..."
                className="h-12 rounded-xl border-border/80 bg-white/90 pl-11 shadow-sm backdrop-blur"
              />
              {showSearchPanel && (
                <div className="absolute left-0 right-0 top-14 overflow-hidden rounded-xl border border-border bg-card shadow-xl">
                  {isSearching && (
                    <div className="flex items-center gap-2 px-4 py-3 text-sm text-muted-foreground">
                      <Loader2 className="size-4 animate-spin" />
                      Searching map locations...
                    </div>
                  )}
                  {!isSearching && searchResults.map((result) => (
                    <button
                      key={result.placeId}
                      type="button"
                      onClick={() => pickSearchResult(result)}
                      className="flex w-full items-start gap-3 px-4 py-3 text-left transition-colors hover:bg-muted/60"
                    >
                      <MapPin className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
                      <span>
                        <span className="block text-sm font-medium text-foreground">{result.label}</span>
                        <span className="block text-xs text-muted-foreground">{result.region} · Suggested code {result.suggestedCode}</span>
                      </span>
                    </button>
                  ))}
                </div>
              )}
            </div>

            <div className="relative mt-5 flex-1 w-full rounded-2xl border border-blue-100 bg-white shadow-sm overflow-hidden z-0 min-h-[300px]">
              {isResolvingMapClick && (
                <div className="absolute left-4 top-4 z-[1000] flex items-center gap-2 rounded-xl border border-border bg-card px-4 py-3 text-sm text-muted-foreground shadow-lg">
                  <Loader2 className="size-4 animate-spin" />
                  Resolving address...
                </div>
              )}
              {open && (
                <MapContainer 
                  center={selected ? [selected.lat, selected.lng] : [20, 0]} 
                  zoom={selected ? 4 : 2} 
                  scrollWheelZoom={true}
                  style={{ height: '100%', width: '100%', position: 'absolute', inset: 0 }}
                >
                  <TileLayer
                    attribution='&copy; <a href="https://www.openstreetmap.org/copyright">OSM</a>'
                    url="https://{s}.basemaps.cartocdn.com/rastertiles/voyager/{z}/{x}/{y}{r}.png"
                  />
                  <MapClickHandler onMapClick={handleMapClick} />
                  <MapUpdater location={selected} />
                  
                  {mapLocations.map((loc) => (
                    <Marker 
                      key={loc.label} 
                      position={[loc.lat, loc.lng]}
                      eventHandlers={{
                        click: () => pickLocation(loc),
                      }}
                      opacity={selected && selected.label !== loc.label ? 0.6 : 1}
                    >
                      <Popup>
                        <div className="font-semibold text-sm">{loc.label}</div>
                        <div className="text-xs text-muted-foreground">{loc.region}</div>
                      </Popup>
                    </Marker>
                  ))}
                </MapContainer>
              )}
            </div>
          </div>
          <aside className="flex shrink-0 flex-col gap-4 border-t border-border bg-card p-5 md:flex-row md:items-center md:justify-between md:p-6">
            <div className="rounded-xl border border-border bg-muted/30 p-4 md:min-w-[360px]">
              <p className="text-xs font-semibold uppercase tracking-[0.16em] text-muted-foreground">Selected</p>
              <p className="mt-2 text-sm font-semibold text-foreground">{selected?.label ?? 'No location selected'}</p>
              <p className="mt-1 text-xs text-muted-foreground">
                {selected ? `${selected.region} · Suggested code ${selected.suggestedCode}` : 'Search a place or click directly on the map.'}
              </p>
            </div>

            <DialogFooter className="shrink-0 flex-row gap-3">
              <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
                Cancel
              </Button>
              <Button type="button" onClick={confirmLocation} disabled={!selected}>
                Use Location
              </Button>
            </DialogFooter>
          </aside>
        </div>
      </DialogContent>
    </Dialog>
  )
}
