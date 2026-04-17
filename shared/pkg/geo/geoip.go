package geo

import (
	"errors"
	"net"

	"github.com/oschwald/geoip2-golang"
	"go.uber.org/zap"
)

// Location holds GeoIP-resolved location data.
type Location struct {
	CountryCode string
	CountryName string
	Region      string
	City        string
	Latitude    float64
	Longitude   float64
	Timezone    string
	ISP         string
}

var ErrNoLocation = errors.New("no location found")

// Resolver provides GeoIP lookups from MaxMind GeoLite2 database.
// The DB is loaded into memory once and queried in-process (~1µs/lookup).
type Resolver struct {
	db  *geoip2.Reader
	log *zap.Logger
}

// NewResolver loads the MaxMind mmdb file.
func NewResolver(dbPath string, log *zap.Logger) (*Resolver, error) {
	db, err := geoip2.Open(dbPath)
	if err != nil {
		log.Warn("geoip database unavailable, location enrichment disabled",
			zap.String("path", dbPath),
			zap.Error(err),
		)
		return &Resolver{db: nil, log: log}, nil
	}
	log.Info("geoip database loaded", zap.String("path", dbPath))
	return &Resolver{db: db, log: log}, nil
}

// Lookup resolves an IP address to location data.
func (r *Resolver) Lookup(ip string) (*Location, error) {
	if r.db == nil {
		return &Location{}, nil
	}

	parsed := net.ParseIP(ip)
	if parsed == nil {
		return &Location{}, nil
	}

	record, err := r.db.City(parsed)
	if err != nil {
		return &Location{}, nil
	}

	loc := &Location{
		CountryCode: record.Country.IsoCode,
		CountryName: record.Country.Names["en"],
		City:        record.City.Names["en"],
		Latitude:    record.Location.Latitude,
		Longitude:   record.Location.Longitude,
		Timezone:    record.Location.TimeZone,
	}

	if len(record.Subdivisions) > 0 {
		loc.Region = record.Subdivisions[0].Names["en"]
	}

	return loc, nil
}

// Close releases the database file handle.
func (r *Resolver) Close() error {
	if r.db != nil {
		return r.db.Close()
	}
	return nil
}

// ExtractIP extracts the real client IP from X-Forwarded-For or RemoteAddr.
func ExtractIP(xForwardedFor, remoteAddr string) string {
	if xForwardedFor != "" {
		// Take the leftmost (original client) IP
		for _, ip := range splitIPs(xForwardedFor) {
			if parsed := net.ParseIP(ip); parsed != nil && !parsed.IsPrivate() {
				return ip
			}
		}
	}
	// Fall back to RemoteAddr
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

func splitIPs(s string) []string {
	var ips []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' || s[i] == ' ' {
			if i > start {
				ips = append(ips, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		ips = append(ips, s[start:])
	}
	return ips
}
