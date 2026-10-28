// Package skillhub provides marketplace clients and safe local installation for skills.
package skillhub

import (
	"context"
	"io"
	"time"
)

type Market string

const (
	MarketYuandun  Market = "yuandun"
	MarketSkillHub Market = "skillhub.cn"
	MarketClawHub  Market = "clawhub.ai"
)

// MarketPriority controls the order in which built-in markets are projected to
// clients. Lower values come first; the 源盾 market intentionally precedes
// SkillHub.cn so it is presented first wherever markets are listed.
func MarketPriority(market Market) int {
	switch market {
	case MarketYuandun:
		return 0
	case MarketSkillHub:
		return 1
	case MarketClawHub:
		return 2
	default:
		return 100
	}
}

type MarketCapabilities struct {
	Search, List, CursorPagination, PagePagination bool
	Categories, Showcase, AuthorFilter, UserSkills bool
	FileList, FileContent, Evaluation              bool
}

type MarketInfo struct {
	ID           Market             `json:"id"`
	Name         string             `json:"name"`
	SiteURL      string             `json:"siteUrl"`
	Capabilities MarketCapabilities `json:"capabilities"`
}

type SearchQuery struct {
	Query             string
	Limit             int
	Page              int
	Cursor            string
	Sort              string
	Order             string
	Category          string
	Author            string
	VerifiedOnly      bool
	NonSuspiciousOnly bool
}

type UserSkillsQuery struct {
	Query string
	Limit int
	Page  int
}

type SearchPage struct {
	Items      []SkillSummary `json:"items"`
	Total      int64          `json:"total,omitempty"`
	Page       int            `json:"page,omitempty"`
	PageSize   int            `json:"pageSize,omitempty"`
	NextCursor string         `json:"nextCursor,omitempty"`
}

type SkillID struct {
	Market Market `json:"market"`
	ID     string `json:"id"`
}

type SkillSummary struct {
	Market             Market          `json:"market"`
	ID                 string          `json:"id"`
	Slug               string          `json:"slug"`
	Name               string          `json:"name"`
	DisplayName        string          `json:"displayName"`
	Description        string          `json:"description"`
	Version            string          `json:"version"`
	Author             string          `json:"author"`
	Category           string          `json:"category"`
	Tags               []string        `json:"tags,omitempty"`
	IconURL            string          `json:"iconUrl,omitempty"`
	Homepage           string          `json:"homepage,omitempty"`
	SourceURL          string          `json:"sourceUrl,omitempty"`
	Source             string          `json:"source,omitempty"`
	PublisherName      string          `json:"publisherName,omitempty"`
	CertifiedName      string          `json:"certifiedName,omitempty"`
	PublisherVerified  bool            `json:"publisherVerified,omitempty"`
	Downloads          int64           `json:"downloads,omitempty"`
	Installs           int64           `json:"installs,omitempty"`
	Stars              int64           `json:"stars,omitempty"`
	Score              float64         `json:"score,omitempty"`
	Verified           bool            `json:"verified,omitempty"`
	Suspicious         bool            `json:"suspicious,omitempty"`
	SecurityStatus     string          `json:"securityStatus,omitempty"`
	SecurityStatusName string          `json:"securityStatusName,omitempty"`
	UpdatedAt          time.Time       `json:"updatedAt,omitempty"`
	Installed          *InstalledState `json:"installed,omitempty"`
}

type SkillDetail struct {
	SkillSummary
	Files           []SkillFile      `json:"files,omitempty"`
	DownloadSources []DownloadSource `json:"downloadSources,omitempty"`
	Readme          string           `json:"readme,omitempty"`
	SecurityReports any              `json:"securityReports,omitempty"`
	Evaluation      any              `json:"evaluation,omitempty"`
}

type DownloadSource struct {
	URL      string `json:"url"`
	Kind     string `json:"kind"`
	Fallback bool   `json:"fallback,omitempty"`
}

type SkillFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256,omitempty"`
	Size   int64  `json:"size,omitempty"`
}

type Category struct {
	Key      string     `json:"key"`
	Name     string     `json:"name"`
	NameEn   string     `json:"nameEn,omitempty"`
	Children []Category `json:"children,omitempty"`
}

type DownloadMeta struct {
	SourceURL string `json:"sourceUrl"`
	Filename  string `json:"filename,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

type InstalledState struct {
	Installed       bool   `json:"installed"`
	Scope           string `json:"scope"`
	Dir             string `json:"dir"`
	Market          Market `json:"market,omitempty"`
	ID              string `json:"id,omitempty"`
	Name            string `json:"name,omitempty"`
	Local           bool   `json:"local,omitempty"`
	Version         string `json:"version,omitempty"`
	Active          bool   `json:"active,omitempty"`
	UpdateAvailable bool   `json:"updateAvailable,omitempty"`
}

type MarketClient interface {
	Market() MarketInfo
	Search(context.Context, SearchQuery) (SearchPage, error)
	UserSkills(context.Context, string, UserSkillsQuery) (SearchPage, error)
	Detail(context.Context, SkillID) (SkillDetail, error)
	Files(context.Context, SkillID, string) ([]SkillFile, error)
	Evaluation(context.Context, SkillID) (any, error)
	DownloadSources(SkillID, string) []DownloadSource
	Download(context.Context, SkillID, string) (io.ReadCloser, DownloadMeta, error)
	Categories(context.Context) ([]Category, error)
}

// ShowcaseClient and FileContentClient are optional market capabilities.
type ShowcaseClient interface {
	Showcase(context.Context, string, SearchQuery) (SearchPage, error)
}
type FileContentClient interface {
	FileContent(context.Context, SkillID, string, string) (string, error)
}
