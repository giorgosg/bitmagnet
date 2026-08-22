package torznab

import "strings"

// availableYes is the Torznab caps value marking a capability as supported.
const availableYes = "yes"

type Profile struct {
	ID                      string `validate:"required"`
	Title                   string
	DisableOrderByRelevance bool
	DefaultLimit            uint
	MaxLimit                uint
	Tags                    []string
}

var ProfileDefault = Profile{
	ID:           "default",
	Title:        "bitmagnet",
	DefaultLimit: 100,
	MaxLimit:     100,
}

func (p Profile) MergeDefaults() Profile {
	if p.Title == "" {
		p.Title = ProfileDefault.Title
	}

	if p.DefaultLimit == 0 {
		p.DefaultLimit = ProfileDefault.DefaultLimit
	}

	if p.MaxLimit == 0 {
		p.MaxLimit = ProfileDefault.MaxLimit
	}

	if p.DefaultLimit > p.MaxLimit {
		p.DefaultLimit = p.MaxLimit
	}

	return p
}

func (p Profile) Caps() Caps {
	return Caps{
		Server: CapsServer{
			Title: p.Title,
		},
		Limits: CapsLimits{
			Max:     p.MaxLimit,
			Default: p.DefaultLimit,
		},
		Searching: CapsSearching{
			Search: CapsSearch{
				Available: availableYes,
				SupportedParams: strings.Join([]string{
					ParamQuery,
					ParamIMDBID,
					ParamTMDBID,
				}, ","),
			},
			TvSearch: CapsSearch{
				Available: availableYes,
				SupportedParams: strings.Join([]string{
					ParamQuery,
					ParamIMDBID,
					ParamTMDBID,
					ParamSeason,
					ParamEpisode,
				}, ","),
			},
			MovieSearch: CapsSearch{
				Available: availableYes,
				SupportedParams: strings.Join([]string{
					ParamQuery,
					ParamIMDBID,
					ParamTMDBID,
				}, ","),
			},
			MusicSearch: CapsSearch{
				Available:       availableYes,
				SupportedParams: ParamQuery,
			},
			AudioSearch: CapsSearch{
				Available: "no",
			},
			BookSearch: CapsSearch{
				Available:       availableYes,
				SupportedParams: ParamQuery,
			},
		},
		Categories: CapsCategories{
			Categories: TopLevelCategories,
		},
	}
}
