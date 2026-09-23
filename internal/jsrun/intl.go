package jsrun

// intl.go holds the Go natives behind js/modules/intl.js: time zones, date
// and time formatting, number formatting and collation. goja has no Intl at
// all, and its own locale methods are silently wrong: Number's
// toLocaleString takes its argument as a radix, Date's use en-GB layouts, and
// localeCompare ignores its locales and options.
//
// The rule throughout is the package's: a result either matches what Node 24
// prints, which the parity goldens in testdata/parity pin, or it is a named
// error. Nothing here falls back to English, or to a default, when it was asked
// for something it cannot do.
//
//   - Dates format in en-US only. Asking for another locale is a RangeError,
//     never English text in place of German.
//   - Numbers format in any locale golang.org/x/text knows, with its CLDR
//     separators and digits, corrected where CLDR has changed since x/text's
//     copy (see numberCorrections). An option it cannot honour is an error.
//   - Collation is x/text's, with the numeric and sensitivity options.
//
// Every native here is bounded: it formats one value, whose size the caller
// already bounded, and none of them loops as far as an argument tells it to.

import (
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/collate"
	"golang.org/x/text/currency"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"golang.org/x/text/number"
	"golang.org/x/text/unicode/norm"
)

func init() {
	registerNative("intl.locales", nativeLocales)
	registerNative("intl.zone", nativeZone)
	registerNative("intl.dateTimeFormat", nativeDateTimeFormat)
	registerNative("intl.formatDate", nativeFormatDate)
	registerNative("intl.numberFormat", nativeNumberFormat)
	registerNative("intl.formatNumber", nativeFormatNumber)
	registerNative("intl.compare", nativeCompare)
	registerNative("intl.weekInfo", nativeWeekInfo)
}

// ---- Locales -----------------------------------------------------------------

// localeSyntax is a Unicode BCP 47 locale identifier: a language, an optional
// script and region, variants, and extensions. x/text's own parser is
// lenient (it takes "en_US"), and Node is not, so the shape is checked first.
var localeSyntax = regexp.MustCompile(`^(?i:[a-z]{2,3}|[a-z]{5,8})(?:-(?i:[a-z]{4}))?(?:-(?i:[a-z]{2}|[0-9]{3}))?(?:-(?i:[a-z0-9]{5,8}|[0-9][a-z0-9]{3}))*(?:-(?i:[0-9a-wyz])(?:-(?i:[a-z0-9]{2,8}))+)*(?:-(?i:x)(?:-(?i:[a-z0-9]{1,8}))+)?$`)

// parseLocale parses one locale identifier, as CanonicalizeLocaleList does.
// A well-formed identifier x/text does not know, such as one naming an
// unassigned region, is still a locale, as it is in Node: it keeps its
// canonical spelling and falls back to its language for data.
func parseLocale(text string) (language.Tag, error) {
	tag, _, err := parseLocaleName(text)
	return tag, err
}

func parseLocaleName(text string) (language.Tag, string, error) {
	if len(text) > 200 || !localeSyntax.MatchString(text) {
		return language.Und, "", rangeError("Incorrect locale information provided")
	}
	if tag, err := language.Parse(text); err == nil {
		return tag, tag.String(), nil
	}
	subtags := strings.Split(strings.ToLower(text), "-")
	for index, subtag := range subtags {
		if index == 0 {
			continue
		}
		if len(subtag) == 1 {
			break // extensions keep their case
		}
		switch {
		case len(subtag) == 4 && index == 1:
			subtags[index] = strings.ToUpper(subtag[:1]) + subtag[1:]
		case len(subtag) == 2:
			subtags[index] = strings.ToUpper(subtag)
		}
	}
	tag, err := language.Parse(subtags[0])
	if err != nil {
		tag = language.Und
	}
	return tag, strings.Join(subtags, "-"), nil
}

// canonicalLocales canonicalises a list of locale identifiers, dropping
// duplicates. The list has already been read into strings by the module.
func canonicalLocales(value any) ([]string, error) {
	list, _ := value.([]any)
	if len(list) > 100 {
		return nil, rangeError("a list of %d locales is more than the 100 one call may handle here", len(list))
	}
	seen := map[string]bool{}
	out := []string{}
	for _, item := range list {
		text, ok := item.(string)
		if !ok {
			return nil, typeError("a locale must be a string")
		}
		_, canonical, err := parseLocaleName(text)
		if err != nil {
			return nil, err
		}
		if !seen[canonical] {
			seen[canonical] = true
			out = append(out, canonical)
		}
	}
	return out, nil
}

func nativeLocales(args []any) (any, error) {
	list, err := canonicalLocales(argument(args, 0))
	if err != nil {
		return nil, err
	}
	out := make([]any, len(list))
	for index, name := range list {
		out[index] = name
	}
	return out, nil
}

// unicodeExtension reads one key of a locale's -u- extension, such as "hc".
func unicodeExtension(tag language.Tag, key string) string {
	return tag.TypeForKey(key)
}

// ---- Time zones ------------------------------------------------------------------

// zoneNames are the English names of a zone, as Node prints them. An empty
// name means Node prints the zone's offset instead, and so does this runtime.
type zoneNames struct {
	longStandard, longDaylight, shortStandard, shortDaylight string
}

type zoneEntry struct {
	// id is the tz database identifier Go loads the zone's rules by, and
	// canonical what resolvedOptions() reports for it.
	id, canonical string
	names         zoneNames
}

// zoneTable maps a zone identifier, in lower case, to its entry. It is built
// from zoneTableText on first use.
var zoneTable = sync.OnceValue(func() map[string]*zoneEntry {
	table := map[string]*zoneEntry{}
	var names zoneNames
	for _, line := range strings.Split(zoneTableText, "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "@") {
			fields := strings.Split(line[1:], "|")
			names = zoneNames{longStandard: fields[0], longDaylight: fields[1], shortStandard: fields[2], shortDaylight: fields[3]}
			continue
		}
		for _, entry := range strings.Fields(line) {
			id, canonical, aliased := strings.Cut(entry, "=")
			if !aliased {
				canonical = id
			}
			table[strings.ToLower(id)] = &zoneEntry{id: id, canonical: canonical, names: names}
		}
	}
	return table
})

// offsetZone is an ECMA-402 offset time zone such as "+07:00" or "-0530".
var offsetZone = regexp.MustCompile(`^([+-])([01][0-9]|2[0-3])(?::?([0-5][0-9]))?$`)

// resolvedZone is a zone ready to format in: its reported name, its rules,
// and its names.
type resolvedZone struct {
	canonical string
	location  *time.Location
	names     zoneNames
}

var zoneLocations sync.Map // tz identifier -> *time.Location

// resolveZone validates a time-zone identifier and loads its rules. Names are
// matched without regard to case, as Node matches them, and reported in the
// form Node reports them.
func resolveZone(name string) (*resolvedZone, error) {
	if match := offsetZone.FindStringSubmatch(name); match != nil {
		hours, _ := strconv.Atoi(match[2])
		minutes := 0
		if match[3] != "" {
			minutes, _ = strconv.Atoi(match[3])
		}
		sign := match[1]
		if hours == 0 && minutes == 0 {
			sign = "+"
		}
		seconds := (hours*60 + minutes) * 60
		if sign == "-" {
			seconds = -seconds
		}
		canonical := fmt.Sprintf("%s%02d:%02d", sign, hours, minutes)
		return &resolvedZone{canonical: canonical, location: time.FixedZone(canonical, seconds)}, nil
	}
	entry := zoneTable()[strings.ToLower(name)]
	id := name
	if entry != nil {
		id = entry.id
	} else if !strings.Contains(name, "/") || strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
		// A zone the table does not list is looked up only by its exact
		// tz name, for zones added to the database after the table was made.
		return nil, rangeError("Invalid time zone specified: %s", name)
	}
	location, err := loadZone(id)
	if err != nil {
		return nil, rangeError("Invalid time zone specified: %s", name)
	}
	if entry == nil {
		return &resolvedZone{canonical: name, location: location}, nil
	}
	return &resolvedZone{canonical: entry.canonical, location: location, names: entry.names}, nil
}

func loadZone(id string) (*time.Location, error) {
	if cached, ok := zoneLocations.Load(id); ok {
		return cached.(*time.Location), nil
	}
	if id == "UTC" {
		return time.UTC, nil
	}
	location, err := time.LoadLocation(id)
	if err != nil {
		return nil, err
	}
	zoneLocations.Store(id, location)
	return location, nil
}

func nativeZone(args []any) (any, error) {
	name, err := argString(args, 0, "timeZone")
	if err != nil {
		return nil, err
	}
	zone, err := resolveZone(name)
	if err != nil {
		return nil, err
	}
	return zone.canonical, nil
}

// offsetName renders an offset as Node does for the shortOffset and
// longOffset styles: "GMT+7", "GMT+5:30", "GMT+07:00".
func offsetName(seconds int, long bool) string {
	sign := "+"
	if seconds < 0 {
		sign, seconds = "-", -seconds
	}
	hours, minutes, rest := seconds/3600, seconds/60%60, seconds%60
	if long {
		text := fmt.Sprintf("GMT%s%02d:%02d", sign, hours, minutes)
		if rest != 0 {
			text += fmt.Sprintf(":%02d", rest)
		}
		return text
	}
	text := fmt.Sprintf("GMT%s%d", sign, hours)
	if minutes != 0 || rest != 0 {
		text += fmt.Sprintf(":%02d", minutes)
	}
	if rest != 0 {
		text += fmt.Sprintf(":%02d", rest)
	}
	return text
}

// zoneName is the zone's name at an instant in one of the timeZoneName
// styles.
func zoneName(at time.Time, zone *resolvedZone, style string) (string, error) {
	_, offset := at.Zone()
	daylight := at.IsDST()
	switch style {
	case "shortOffset":
		return offsetName(offset, false), nil
	case "longOffset":
		return offsetName(offset, true), nil
	case "short":
		name := zone.names.shortStandard
		if daylight {
			name = zone.names.shortDaylight
		}
		if name == "" {
			return offsetName(offset, false), nil
		}
		return name, nil
	case "long":
		name := zone.names.longStandard
		if daylight {
			name = zone.names.longDaylight
		}
		if name == "" {
			return offsetName(offset, true), nil
		}
		return name, nil
	}
	return "", rangeError("timeZoneName %s is not supported", style)
}

// BEGIN zone table (generated by scripts/js-parity/record.mjs; do not edit)
const zoneTableText = `
@Acre Standard Time|||
America/Eirunepe America/Porto_Acre=America/Rio_Branco America/Rio_Branco Brazil/Acre=America/Rio_Branco
@Afghanistan Time|||
Asia/Kabul
@Alaska Standard Time|Alaska Daylight Time|AKST|AKDT
America/Anchorage America/Juneau America/Metlakatla America/Nome America/Sitka America/Yakutat
US/Alaska=America/Anchorage
@Amazon Standard Time|||
America/Boa_Vista America/Campo_Grande America/Cuiaba America/Manaus America/Porto_Velho
Brazil/West=America/Manaus
@American Samoa Standard Time|||
Pacific/Midway Pacific/Pago_Pago Pacific/Samoa=Pacific/Pago_Pago US/Samoa=Pacific/Pago_Pago
@Arabian Standard Time|||
Asia/Aden Asia/Baghdad Asia/Bahrain Asia/Kuwait Asia/Qatar Asia/Riyadh
@Argentina Standard Time|||
America/Argentina/Buenos_Aires=America/Buenos_Aires America/Argentina/Catamarca=America/Catamarca
America/Argentina/ComodRivadavia=America/Catamarca America/Argentina/Cordoba=America/Cordoba
America/Argentina/Jujuy=America/Jujuy America/Argentina/La_Rioja America/Argentina/Mendoza=America/Mendoza
America/Argentina/Rio_Gallegos America/Argentina/Salta America/Argentina/San_Juan America/Argentina/San_Luis
America/Argentina/Tucuman America/Argentina/Ushuaia America/Buenos_Aires America/Catamarca America/Cordoba
America/Jujuy America/Mendoza America/Rosario=America/Cordoba
@Armenia Standard Time|||
Asia/Yerevan
@Atlantic Standard Time|Atlantic Daylight Time|AST|ADT
America/Anguilla America/Antigua America/Aruba America/Barbados America/Blanc-Sablon America/Curacao
America/Dominica America/Glace_Bay America/Goose_Bay America/Grenada America/Guadeloupe America/Halifax
America/Kralendijk America/Lower_Princes America/Marigot America/Martinique America/Moncton America/Montserrat
America/Port_of_Spain America/Puerto_Rico America/Santo_Domingo America/St_Barthelemy America/St_Kitts
America/St_Lucia America/St_Thomas America/St_Vincent America/Thule America/Tortola
America/Virgin=America/St_Thomas Atlantic/Bermuda Canada/Atlantic=America/Halifax
@Australian Central Standard Time|Australian Central Daylight Time||
Australia/Adelaide Australia/Broken_Hill Australia/Darwin Australia/North=Australia/Darwin
Australia/South=Australia/Adelaide Australia/Yancowinna=Australia/Broken_Hill
@Australian Central Western Standard Time|||
Australia/Eucla
@Australian Eastern Standard Time|Australian Eastern Daylight Time||
Antarctica/Macquarie Australia/ACT=Australia/Sydney Australia/Brisbane Australia/Canberra=Australia/Sydney
Australia/Currie=Australia/Hobart Australia/Hobart Australia/Lindeman Australia/Melbourne
Australia/NSW=Australia/Sydney Australia/Queensland=Australia/Brisbane Australia/Sydney
Australia/Tasmania=Australia/Hobart Australia/Victoria=Australia/Melbourne
@Australian Western Standard Time|||
Antarctica/Casey Australia/Perth Australia/West=Australia/Perth
@Azerbaijan Standard Time|||
Asia/Baku
@Azores Standard Time|Azores Summer Time||
Atlantic/Azores
@Bangladesh Standard Time|||
Asia/Dacca=Asia/Dhaka Asia/Dhaka
@Bhutan Time|||
Asia/Thimbu=Asia/Thimphu Asia/Thimphu
@Bolivia Time|||
America/La_Paz
@Brasilia Standard Time|||
America/Araguaina America/Bahia America/Belem America/Fortaleza America/Maceio America/Recife America/Santarem
America/Sao_Paulo Brazil/East=America/Sao_Paulo
@Brunei Time|||
Asia/Brunei
@Cape Verde Standard Time|||
Atlantic/Cape_Verde
@Central Africa Time|||
Africa/Blantyre Africa/Bujumbura Africa/Gaborone Africa/Harare Africa/Juba Africa/Khartoum Africa/Kigali
Africa/Lubumbashi Africa/Lusaka Africa/Maputo Africa/Windhoek
@Central European Standard Time|Central European Summer Time||
Africa/Algiers Africa/Ceuta Africa/Tunis Arctic/Longyearbyen Atlantic/Jan_Mayen=Arctic/Longyearbyen
CET=Europe/Brussels Europe/Amsterdam Europe/Andorra Europe/Belgrade Europe/Berlin Europe/Bratislava
Europe/Brussels Europe/Budapest Europe/Busingen Europe/Copenhagen Europe/Gibraltar Europe/Ljubljana
Europe/Luxembourg Europe/Madrid Europe/Malta Europe/Monaco Europe/Oslo Europe/Paris Europe/Podgorica
Europe/Prague Europe/Rome Europe/San_Marino Europe/Sarajevo Europe/Skopje Europe/Stockholm Europe/Tirane
Europe/Vaduz Europe/Vatican Europe/Vienna Europe/Warsaw Europe/Zagreb Europe/Zurich MET=Europe/Brussels
Poland=Europe/Warsaw
@Central Indonesia Time|||
Asia/Makassar Asia/Ujung_Pandang=Asia/Makassar
@Central Standard Time|Central Daylight Time|CST|CDT
America/Bahia_Banderas America/Belize America/Chicago America/Chihuahua America/Costa_Rica America/El_Salvador
America/Guatemala America/Indiana/Knox America/Indiana/Tell_City America/Knox_IN=America/Indiana/Knox
America/Managua America/Matamoros America/Menominee America/Merida America/Mexico_City America/Monterrey
America/North_Dakota/Beulah America/North_Dakota/Center America/North_Dakota/New_Salem America/Ojinaga
America/Rainy_River=America/Winnipeg America/Rankin_Inlet America/Regina America/Resolute
America/Swift_Current America/Tegucigalpa America/Winnipeg CST6CDT=America/Chicago
Canada/Central=America/Winnipeg Canada/Saskatchewan=America/Regina Mexico/General=America/Mexico_City
US/Central=America/Chicago US/Indiana-Starke=America/Indiana/Knox
@Chamorro Standard Time|||
Pacific/Guam Pacific/Saipan
@Chatham Standard Time|Chatham Daylight Time||
NZ-CHAT=Pacific/Chatham Pacific/Chatham
@Chile Standard Time|Chile Summer Time||
America/Santiago Chile/Continental=America/Santiago
@China Standard Time|||
Asia/Chongqing=Asia/Shanghai Asia/Chungking=Asia/Shanghai Asia/Harbin=Asia/Shanghai Asia/Macao=Asia/Macau
Asia/Macau Asia/Shanghai PRC=Asia/Shanghai
@Christmas Island Time|||
Indian/Christmas
@Chuuk Time|||
Pacific/Chuuk=Pacific/Truk Pacific/Truk Pacific/Yap=Pacific/Truk
@Cocos Islands Time|||
Indian/Cocos
@Colombia Standard Time|||
America/Bogota
@Cook Islands Standard Time|||
Pacific/Rarotonga
@Coordinated Universal Time||UTC|
Etc/GMT=UTC Etc/UCT=UTC Etc/UTC=UTC Etc/Universal=UTC Etc/Zulu=UTC GMT=UTC GMT+0=UTC GMT-0=UTC GMT0=UTC
UCT=UTC UTC Universal=UTC Zulu=UTC
@Cuba Standard Time|Cuba Daylight Time||
America/Havana Cuba=America/Havana
@Davis Time|||
Antarctica/Davis
@Dumont d’Urville Time|||
Antarctica/DumontDUrville
@East Africa Time|||
Africa/Addis_Ababa Africa/Asmara=Africa/Asmera Africa/Asmera Africa/Dar_es_Salaam Africa/Djibouti
Africa/Kampala Africa/Mogadishu Africa/Nairobi Indian/Antananarivo Indian/Comoro Indian/Mayotte
@Easter Island Standard Time|Easter Island Summer Time||
Chile/EasterIsland=Pacific/Easter Pacific/Easter
@Eastern European Standard Time|Eastern European Summer Time||
Africa/Cairo Africa/Tripoli Asia/Beirut Asia/Famagusta Asia/Gaza Asia/Hebron Asia/Nicosia EET=Europe/Athens
Egypt=Africa/Cairo Europe/Athens Europe/Bucharest Europe/Chisinau Europe/Helsinki Europe/Kaliningrad
Europe/Kiev Europe/Kyiv=Europe/Kiev Europe/Mariehamn Europe/Nicosia=Asia/Nicosia Europe/Riga Europe/Sofia
Europe/Tallinn Europe/Tiraspol=Europe/Chisinau Europe/Uzhgorod=Europe/Kiev Europe/Vilnius
Europe/Zaporozhye=Europe/Kiev Libya=Africa/Tripoli
@Eastern Indonesia Time|||
Asia/Jayapura
@Eastern Standard Time|Eastern Daylight Time|EST|EDT
America/Atikokan=America/Coral_Harbour America/Cancun America/Cayman America/Coral_Harbour America/Detroit
America/Fort_Wayne=America/Indianapolis America/Grand_Turk America/Indiana/Indianapolis=America/Indianapolis
America/Indiana/Marengo America/Indiana/Petersburg America/Indiana/Vevay America/Indiana/Vincennes
America/Indiana/Winamac America/Indianapolis America/Iqaluit America/Jamaica
America/Kentucky/Louisville=America/Louisville America/Kentucky/Monticello America/Louisville
America/Montreal=America/Toronto America/Nassau America/New_York America/Nipigon=America/Toronto
America/Panama America/Pangnirtung=America/Iqaluit America/Port-au-Prince America/Thunder_Bay=America/Toronto
America/Toronto Canada/Eastern=America/Toronto EST=America/Panama EST5EDT=America/New_York
Jamaica=America/Jamaica US/East-Indiana=America/Indianapolis US/Eastern=America/New_York
US/Michigan=America/Detroit
@Ecuador Time|||
America/Guayaquil
@Falkland Islands Standard Time|||
Atlantic/Stanley
@Fernando de Noronha Standard Time|||
America/Noronha Brazil/DeNoronha=America/Noronha
@Fiji Standard Time|||
Pacific/Fiji
@French Guiana Time|||
America/Cayenne
@French Southern & Antarctic Time|||
Indian/Kerguelen
@Galapagos Time|||
Pacific/Galapagos
@Gambier Time|||
Pacific/Gambier
@Georgia Standard Time|||
Asia/Tbilisi
@Gilbert Islands Time|||
Pacific/Tarawa
@Greenland Standard Time|Greenland Summer Time||
America/Godthab America/Nuuk=America/Godthab America/Scoresbysund
@Greenwich Mean Time|British Summer Time|GMT|
Europe/Belfast=Europe/London Europe/London GB=Europe/London GB-Eire=Europe/London
@Greenwich Mean Time|Irish Standard Time|GMT|
Eire=Europe/Dublin Europe/Dublin
@Greenwich Mean Time||GMT|
Africa/Abidjan Africa/Accra Africa/Bamako Africa/Banjul Africa/Bissau Africa/Conakry Africa/Dakar
Africa/Freetown Africa/Lome Africa/Monrovia Africa/Nouakchott Africa/Ouagadougou Africa/Sao_Tome
Africa/Timbuktu=Africa/Bamako America/Danmarkshavn Antarctica/Troll Atlantic/Reykjavik Atlantic/St_Helena
Etc/GMT+0=UTC Etc/GMT-0=UTC Etc/GMT0=UTC Etc/Greenwich=UTC Europe/Guernsey Europe/Isle_of_Man Europe/Jersey
Greenwich=UTC Iceland=Atlantic/Reykjavik
@Gulf Standard Time|||
Asia/Dubai Asia/Muscat
@Guyana Time|||
America/Guyana
@Hawaii-Aleutian Standard Time|Hawaii-Aleutian Daylight Time|HAST|HADT
America/Adak America/Atka=America/Adak US/Aleutian=America/Adak
@Hawaii-Aleutian Standard Time||HST|
HST=Pacific/Honolulu Pacific/Honolulu Pacific/Johnston=Pacific/Honolulu US/Hawaii=Pacific/Honolulu
@Hong Kong Standard Time|||
Asia/Hong_Kong Hongkong=Asia/Hong_Kong
@India Standard Time|||
Asia/Calcutta Asia/Colombo Asia/Kolkata=Asia/Calcutta
@Indian Ocean Time|||
Indian/Chagos
@Indochina Time|||
Asia/Bangkok Asia/Ho_Chi_Minh=Asia/Saigon Asia/Phnom_Penh Asia/Saigon Asia/Vientiane
@Iran Standard Time|||
Asia/Tehran Iran=Asia/Tehran
@Irkutsk Standard Time|||
Asia/Irkutsk
@Israel Standard Time|Israel Daylight Time||
Asia/Jerusalem Asia/Tel_Aviv=Asia/Jerusalem Israel=Asia/Jerusalem
@Japan Standard Time|||
Asia/Tokyo Japan=Asia/Tokyo
@Kamchatka Standard Time|||
Asia/Anadyr Asia/Kamchatka
@Kazakhstan Time|||
Asia/Almaty Asia/Aqtau Asia/Aqtobe Asia/Atyrau Asia/Oral Asia/Qostanay Asia/Qyzylorda
@Khovd Standard Time|||
Asia/Hovd
@Korean Standard Time|||
Asia/Pyongyang Asia/Seoul ROK=Asia/Seoul
@Kosrae Time|||
Pacific/Kosrae
@Krasnoyarsk Standard Time|||
Asia/Barnaul Asia/Krasnoyarsk Asia/Novokuznetsk Asia/Novosibirsk Asia/Tomsk
@Kyrgyzstan Time|||
Asia/Bishkek
@Line Islands Time|||
Pacific/Kiritimati
@Lord Howe Standard Time|Lord Howe Daylight Time||
Australia/LHI=Australia/Lord_Howe Australia/Lord_Howe
@Magadan Standard Time|||
Asia/Magadan Asia/Sakhalin Asia/Srednekolymsk
@Malaysia Time|||
Asia/Kuala_Lumpur Asia/Kuching
@Maldives Time|||
Indian/Maldives
@Marquesas Time|||
Pacific/Marquesas
@Marshall Islands Time|||
Kwajalein=Pacific/Kwajalein Pacific/Kwajalein Pacific/Majuro
@Mauritius Standard Time|||
Indian/Mauritius
@Mawson Time|||
Antarctica/Mawson
@Mexican Pacific Standard Time|||
America/Hermosillo America/Mazatlan Mexico/BajaSur=America/Mazatlan
@Moscow Standard Time|||
Europe/Kirov Europe/Minsk Europe/Moscow Europe/Simferopol Europe/Volgograd W-SU=Europe/Moscow
@Mountain Standard Time|Mountain Daylight Time|MST|MDT
America/Boise America/Cambridge_Bay America/Ciudad_Juarez America/Creston America/Dawson_Creek America/Denver
America/Edmonton America/Fort_Nelson America/Inuvik America/Phoenix America/Shiprock=America/Denver
America/Yellowknife=America/Edmonton Canada/Mountain=America/Edmonton MST=America/Phoenix
MST7MDT=America/Denver Navajo=America/Denver US/Arizona=America/Phoenix US/Mountain=America/Denver
@Myanmar Time|||
Asia/Rangoon Asia/Yangon=Asia/Rangoon
@Nauru Time|||
Pacific/Nauru
@Nepal Time|||
Asia/Kathmandu=Asia/Katmandu Asia/Katmandu
@New Caledonia Standard Time|||
Pacific/Noumea
@New Zealand Standard Time|New Zealand Daylight Time||
Antarctica/McMurdo Antarctica/South_Pole=Antarctica/McMurdo NZ=Pacific/Auckland Pacific/Auckland
@Newfoundland Standard Time|Newfoundland Daylight Time||
America/St_Johns Canada/Newfoundland=America/St_Johns
@Niue Time|||
Pacific/Niue
@Norfolk Island Standard Time|Norfolk Island Daylight Time||
Pacific/Norfolk
@Omsk Standard Time|||
Asia/Omsk
@Pacific Standard Time|Pacific Daylight Time|PST|PDT
America/Ensenada=America/Tijuana America/Los_Angeles America/Santa_Isabel=America/Tijuana America/Tijuana
America/Vancouver Canada/Pacific=America/Vancouver Mexico/BajaNorte=America/Tijuana
PST8PDT=America/Los_Angeles US/Pacific=America/Los_Angeles
@Pakistan Standard Time|||
Asia/Karachi
@Palau Time|||
Pacific/Palau
@Papua New Guinea Time|||
Pacific/Port_Moresby
@Paraguay Standard Time|||
America/Asuncion
@Peru Standard Time|||
America/Lima
@Philippine Standard Time|||
Asia/Manila
@Phoenix Islands Time|||
Pacific/Enderbury Pacific/Kanton=Pacific/Enderbury
@Pitcairn Time|||
Pacific/Pitcairn
@Pohnpei Time|||
Pacific/Pohnpei=Pacific/Ponape Pacific/Ponape
@Rothera Time|||
Antarctica/Rothera
@Réunion Time|||
Indian/Reunion
@Samara Standard Time|||
Europe/Astrakhan Europe/Samara Europe/Saratov Europe/Ulyanovsk
@Samoa Standard Time|||
Pacific/Apia
@Seychelles Time|||
Indian/Mahe
@Singapore Standard Time|||
Asia/Singapore Singapore=Asia/Singapore
@Solomon Islands Time|||
Pacific/Guadalcanal
@South Africa Standard Time|||
Africa/Johannesburg Africa/Maseru Africa/Mbabane
@South Georgia Time|||
Atlantic/South_Georgia
@St. Pierre & Miquelon Standard Time|St. Pierre & Miquelon Daylight Time||
America/Miquelon
@Suriname Time|||
America/Paramaribo
@Syowa Time|||
Antarctica/Syowa
@Tahiti Time|||
Pacific/Tahiti
@Taiwan Standard Time|||
Asia/Taipei ROC=Asia/Taipei
@Tajikistan Time|||
Asia/Dushanbe
@Timor-Leste Time|||
Asia/Dili
@Tokelau Time|||
Pacific/Fakaofo
@Tonga Standard Time|||
Pacific/Tongatapu
@Turkmenistan Standard Time|||
Asia/Ashgabat Asia/Ashkhabad=Asia/Ashgabat
@Tuvalu Time|||
Pacific/Funafuti
@Türkiye Standard Time|||
Asia/Istanbul=Europe/Istanbul Europe/Istanbul Turkey=Europe/Istanbul
@Ulaanbaatar Standard Time|||
Asia/Choibalsan=Asia/Ulaanbaatar Asia/Ulaanbaatar Asia/Ulan_Bator=Asia/Ulaanbaatar
@Uruguay Standard Time|||
America/Montevideo
@Uzbekistan Standard Time|||
Asia/Samarkand Asia/Tashkent
@Vanuatu Standard Time|||
Pacific/Efate
@Venezuela Time|||
America/Caracas
@Vladivostok Standard Time|||
Asia/Ust-Nera Asia/Vladivostok
@Vostok Time|||
Antarctica/Vostok
@Wake Island Time|||
Pacific/Wake
@Wallis & Futuna Time|||
Pacific/Wallis
@West Africa Time|||
Africa/Bangui Africa/Brazzaville Africa/Douala Africa/Kinshasa Africa/Lagos Africa/Libreville Africa/Luanda
Africa/Malabo Africa/Ndjamena Africa/Niamey Africa/Porto-Novo
@Western European Standard Time|Western European Summer Time||
Atlantic/Canary Atlantic/Faeroe Atlantic/Faroe=Atlantic/Faeroe Atlantic/Madeira Europe/Lisbon
Portugal=Europe/Lisbon WET=Europe/Lisbon
@Western Indonesia Time|||
Asia/Jakarta Asia/Pontianak
@Yakutsk Standard Time|||
Asia/Chita Asia/Khandyga Asia/Yakutsk
@Yekaterinburg Standard Time|||
Asia/Yekaterinburg
@Yukon Time|||
America/Dawson America/Whitehorse Canada/Yukon=America/Whitehorse
@|||
Africa/Casablanca Africa/El_Aaiun America/Coyhaique America/Punta_Arenas Antarctica/Palmer Asia/Amman
Asia/Damascus Asia/Kashgar=Asia/Urumqi Asia/Urumqi Etc/GMT+1 Etc/GMT+10 Etc/GMT+11 Etc/GMT+12 Etc/GMT+2
Etc/GMT+3 Etc/GMT+4 Etc/GMT+5 Etc/GMT+6 Etc/GMT+7 Etc/GMT+8 Etc/GMT+9 Etc/GMT-1 Etc/GMT-10 Etc/GMT-11
Etc/GMT-12 Etc/GMT-13 Etc/GMT-14 Etc/GMT-2 Etc/GMT-3 Etc/GMT-4 Etc/GMT-5 Etc/GMT-6 Etc/GMT-7 Etc/GMT-8
Etc/GMT-9 Pacific/Bougainville
`

// END zone table

// ---- Date and time formatting ------------------------------------------------------

// A date is formatted the way ECMA-402 describes and ICU implements: the
// options become a skeleton, the skeleton is matched against the locale's
// available patterns, and the pattern is rendered. The matching follows the
// Unicode date-pattern generator (UTS #35) with the CLDR data of en-US, and
// the option sweep in testdata/parity/date-options.json pins its output.

type dateField int

const (
	fieldEra dateField = iota
	fieldYear
	fieldMonth
	fieldWeekday
	fieldDay
	fieldDayPeriod
	fieldHour
	fieldMinute
	fieldSecond
	fieldFraction
	fieldZone
	fieldCount
)

// dateMask covers the fields a date part of a pattern holds; the rest are the
// time part.
const dateMask = 1<<fieldDayPeriod - 1

func fieldOf(char byte) (dateField, bool) {
	switch char {
	case 'G':
		return fieldEra, true
	case 'y':
		return fieldYear, true
	case 'M', 'L':
		return fieldMonth, true
	case 'E', 'c':
		return fieldWeekday, true
	case 'd':
		return fieldDay, true
	case 'a', 'B':
		return fieldDayPeriod, true
	case 'h', 'H', 'K', 'k':
		return fieldHour, true
	case 'm':
		return fieldMinute, true
	case 's':
		return fieldSecond, true
	case 'S':
		return fieldFraction, true
	case 'z', 'O', 'v':
		return fieldZone, true
	}
	return 0, false
}

// The distance between two widths of a field. Text and numeric forms are far
// apart; within numeric forms the length counts.
const (
	widthNarrow  = -0x101
	widthShorter = -0x102
	widthShort   = -0x103
	widthLong    = -0x104
	widthNumeric = 0x100
	widthDelta   = 0x10

	distanceMissing = 0x1000
	distanceExtra   = 0x10000
)

func textWidth(size int) int {
	switch {
	case size <= 3:
		return widthShort
	case size == 4:
		return widthLong
	case size == 5:
		return widthNarrow
	}
	return widthShorter
}

// widthOf is a field's position on the width scale, which is what the
// matcher measures distance on.
func widthOf(char byte, size int) int {
	switch char {
	case 'G', 'E', 'a', 'z':
		return textWidth(size)
	case 'B':
		return textWidth(size) - 3*widthDelta
	case 'M':
		if size <= 2 {
			return widthNumeric + size
		}
		return textWidth(size)
	case 'L':
		if size <= 2 {
			return widthNumeric + widthDelta + size
		}
		return textWidth(size) - widthDelta
	case 'c':
		if size <= 2 {
			return widthNumeric + 2*widthDelta + size
		}
		return textWidth(size) - 2*widthDelta
	case 'y', 'd', 'h', 'm', 's':
		return widthNumeric + size
	case 'K', 'S':
		return widthNumeric + widthDelta + size
	case 'H':
		return widthNumeric + 10*widthDelta + size
	case 'k':
		return widthNumeric + 11*widthDelta + size
	case 'O':
		return textWidth(size) - widthDelta
	case 'v':
		return textWidth(size) - 2*widthDelta
	}
	return 0
}

// skeleton is the set of fields a pattern shows, each with its character and
// length.
type skeleton struct {
	char [fieldCount]byte
	size [fieldCount]int
}

func (s *skeleton) set(char byte, size int) {
	if field, ok := fieldOf(char); ok {
		s.char[field], s.size[field] = char, size
	}
}

func (s skeleton) has(field dateField) bool { return s.char[field] != 0 }

func (s skeleton) mask() int {
	mask := 0
	for field := dateField(0); field < fieldCount; field++ {
		if s.has(field) {
			mask |= 1 << field
		}
	}
	return mask
}

func (s skeleton) width(field dateField) int {
	if !s.has(field) {
		return 0
	}
	return widthOf(s.char[field], s.size[field])
}

// forMatching adds the day period a 12-hour clock implies, and drops one a
// 24-hour clock ignores, as the pattern generator does. Minutes with
// fractional seconds and no seconds get seconds, which is the one gap the
// generator fills.
func (s skeleton) forMatching() skeleton {
	if s.has(fieldMinute) && s.has(fieldFraction) && !s.has(fieldSecond) {
		s.char[fieldSecond], s.size[fieldSecond] = 's', 1
	}
	switch s.char[fieldHour] {
	case 'h', 'K':
		if !s.has(fieldDayPeriod) {
			s.char[fieldDayPeriod], s.size[fieldDayPeriod] = 'a', 1
		}
	case 'H', 'k':
		s.char[fieldDayPeriod], s.size[fieldDayPeriod] = 0, 0
	}
	return s
}

// patternToken is a field (char repeated size times) or literal text.
type patternToken struct {
	char    byte
	size    int
	literal string
}

// parsePattern reads an LDML pattern: letters are fields, and text in single
// quotes is literal (” is a quote).
func parsePattern(pattern string) []patternToken {
	var tokens []patternToken
	literal := func(text string) {
		if count := len(tokens); count > 0 && tokens[count-1].char == 0 {
			tokens[count-1].literal += text
			return
		}
		tokens = append(tokens, patternToken{literal: text})
	}
	for index := 0; index < len(pattern); {
		char := pattern[index]
		switch {
		case char == '\'':
			end := index + 1
			var text strings.Builder
			for end < len(pattern) {
				if pattern[end] == '\'' {
					if end+1 < len(pattern) && pattern[end+1] == '\'' {
						text.WriteByte('\'')
						end += 2
						continue
					}
					break
				}
				text.WriteByte(pattern[end])
				end++
			}
			if end == index+1 && end < len(pattern) {
				text.WriteByte('\'') // '' outside quotes
			}
			literal(text.String())
			index = end + 1
		case char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z':
			end := index
			for end < len(pattern) && pattern[end] == char {
				end++
			}
			tokens = append(tokens, patternToken{char: char, size: end - index})
			index = end
		default:
			_, width := utf8.DecodeRuneInString(pattern[index:])
			literal(pattern[index : index+width])
			index += width
		}
	}
	return tokens
}

// formatPattern writes tokens back as an LDML pattern.
func formatPattern(tokens []patternToken) string {
	var out strings.Builder
	for _, token := range tokens {
		if token.char != 0 {
			out.WriteString(strings.Repeat(string(token.char), token.size))
			continue
		}
		if token.literal == "" {
			continue
		}
		out.WriteByte('\'')
		out.WriteString(strings.ReplaceAll(token.literal, "'", "''"))
		out.WriteByte('\'')
	}
	return out.String()
}

func skeletonOfTokens(tokens []patternToken) skeleton {
	var s skeleton
	for _, token := range tokens {
		if token.char != 0 {
			s.set(token.char, token.size)
		}
	}
	return s
}

func skeletonOfText(text string) skeleton {
	var s skeleton
	for index := 0; index < len(text); {
		end := index
		for end < len(text) && text[end] == text[index] {
			end++
		}
		s.set(text[index], end-index)
		index = end
	}
	return s
}

// available is one entry of a locale's available patterns.
type available struct {
	skeleton skeleton
	tokens   []patternToken
}

// dateLocale is the CLDR data one locale formats dates with.
type dateLocale struct {
	tag string
	// available are the locale's patterns, in the order the matcher tries
	// them: by their most significant field, as the generator iterates.
	available []available
	// dateStyles and timeStyles are the full, long, medium and short
	// patterns; dateTimeStyles join a date and a time for each date style.
	dateStyles, timeStyles, dateTimeStyles [4]string
	appendItems                            [fieldCount]string
	fieldNames                             [fieldCount]string
	months, monthsShort, monthsNarrow      [12]string
	weekdays, weekdaysShort                [7]string
	weekdaysNarrow, weekdaysShorter        [7]string
	eras, erasLong, erasNarrow             [2]string
	amPM                                   [2]string
	// dayPeriods names the flexible day periods, "in the morning" and so
	// on, by the minutes of the day they span; noon is its own.
	dayPeriods       []dayPeriod
	noon, noonNarrow string
	decimal          string
}

type dayPeriod struct {
	from, to int // minutes of the day, [from, to)
	name     string
}

var styleIndex = map[string]int{"full": 0, "long": 1, "medium": 2, "short": 3}

func newDateLocale(tag string, formats [][2]string) *dateLocale {
	locale := &dateLocale{tag: tag}
	type ranked struct {
		entry available
		first dateField
		order int
	}
	var list []ranked
	for order, format := range formats {
		s := skeletonOfText(format[0]).forMatching()
		first := fieldCount
		for field := dateField(0); field < fieldCount; field++ {
			if s.has(field) {
				first = field
				break
			}
		}
		list = append(list, ranked{available{skeleton: s, tokens: parsePattern(format[1])}, first, order})
	}
	// Stable by most significant field, then as written.
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && (list[j].first < list[j-1].first || list[j].first == list[j-1].first && list[j].order < list[j-1].order); j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
	for _, entry := range list {
		locale.available = append(locale.available, entry.entry)
	}
	return locale
}

// enUS is en-US's date data, from CLDR as Node 24 (ICU 78) renders it.
var enUS = sync.OnceValue(func() *dateLocale {
	locale := newDateLocale("en-US", [][2]string{
		{"G", "G"}, {"Gy", "y G"}, {"GyM", "M/y G"}, {"GyMd", "M/d/y G"}, {"GyMEd", "E, M/d/y G"}, {"GyMMM", "MMM y G"}, {"GyMMMd", "MMM d, y G"}, {"GyMMMEd", "E, MMM d, y G"},
		{"y", "y"}, {"yM", "M/y"}, {"yMd", "M/d/y"}, {"yMEd", "E, M/d/y"}, {"yMMM", "MMM y"}, {"yMMMd", "MMM d, y"},
		{"yMMMEd", "E, MMM d, y"}, {"yMMMM", "MMMM y"},
		{"M", "L"}, {"Md", "M/d"}, {"MEd", "E, M/d"}, {"MMM", "LLL"}, {"MMMd", "MMM d"}, {"MMMEd", "E, MMM d"}, {"MMMMd", "MMMM d"},
		{"E", "ccc"}, {"Ed", "d E"}, {"Eh", "E h\u202fa"}, {"EBh", "E h B"}, {"EBhm", "E h:mm B"}, {"EBhms", "E h:mm:ss B"},
		{"Ehm", "E h:mm\u202fa"}, {"EHm", "E HH:mm"}, {"Ehms", "E h:mm:ss\u202fa"}, {"EHms", "E HH:mm:ss"},
		{"d", "d"},
		{"a", "a"}, {"Bh", "h B"}, {"Bhm", "h:mm B"}, {"Bhms", "h:mm:ss B"},
		{"h", "h\u202fa"}, {"H", "HH"}, {"hm", "h:mm\u202fa"}, {"Hm", "HH:mm"}, {"hms", "h:mm:ss\u202fa"}, {"Hms", "HH:mm:ss"},
		{"hmsv", "h:mm:ss\u202fa v"}, {"Hmsv", "HH:mm:ss v"}, {"hmv", "h:mm\u202fa v"}, {"Hmv", "HH:mm v"}, {"hv", "h\u202fa v"}, {"Hv", "HH v"},
		{"m", "m"}, {"ms", "mm:ss"}, {"s", "s"}, {"S", "S"}, {"v", "v"},
	})
	locale.dateStyles = [4]string{"EEEE, MMMM d, y", "MMMM d, y", "MMM d, y", "M/d/yy"}
	locale.timeStyles = [4]string{"h:mm:ss\u202fa zzzz", "h:mm:ss\u202fa z", "h:mm:ss\u202fa", "h:mm\u202fa"}
	locale.dateTimeStyles = [4]string{"{1} 'at' {0}", "{1} 'at' {0}", "{1}, {0}", "{1}, {0}"}
	for field, item := range map[dateField]string{
		fieldEra: "{0} {1}", fieldYear: "{0} {1}", fieldMonth: "{0} ({2}: {1})", fieldWeekday: "{0} {1}", fieldDay: "{0} ({2}: {1})",
		fieldDayPeriod: "{0} \u251c{2}: {1}\u2524", fieldHour: "{0} ({2}: {1})", fieldMinute: "{0} ({2}: {1})", fieldSecond: "{0} ({2}: {1})",
		fieldFraction: "{0} \u251c{2}: {1}\u2524", fieldZone: "{0} {1}",
	} {
		locale.appendItems[field] = item
	}
	for field, name := range map[dateField]string{
		fieldEra: "era", fieldYear: "year", fieldMonth: "month", fieldWeekday: "day of the week", fieldDay: "day",
		fieldDayPeriod: "AM/PM", fieldHour: "hour", fieldMinute: "minute", fieldSecond: "second", fieldFraction: "F14", fieldZone: "time zone",
	} {
		locale.fieldNames[field] = name
	}
	locale.months = [12]string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}
	locale.monthsShort = [12]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
	locale.monthsNarrow = [12]string{"J", "F", "M", "A", "M", "J", "J", "A", "S", "O", "N", "D"}
	locale.weekdays = [7]string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
	locale.weekdaysShort = [7]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	locale.weekdaysNarrow = [7]string{"S", "M", "T", "W", "T", "F", "S"}
	locale.weekdaysShorter = [7]string{"Su", "Mo", "Tu", "We", "Th", "Fr", "Sa"}
	locale.eras = [2]string{"BC", "AD"}
	locale.erasLong = [2]string{"Before Christ", "Anno Domini"}
	locale.erasNarrow = [2]string{"B", "A"}
	locale.amPM = [2]string{"AM", "PM"}
	// ICU reads the night as ending at midnight: 00:00 to 12:00 is all
	// "in the morning".
	locale.dayPeriods = []dayPeriod{
		{0, 12 * 60, "in the morning"}, {12 * 60, 18 * 60, "in the afternoon"},
		{18 * 60, 21 * 60, "in the evening"}, {21 * 60, 24 * 60, "at night"},
	}
	locale.noon, locale.noonNarrow = "noon", "n"
	locale.decimal = "."
	return locale
})

// match is the result of matching a skeleton against one available pattern.
type match struct {
	distance       int
	missing, extra int
}

// distance measures how far an available skeleton is from the requested one
// over the fields in include.
func distance(requested, candidate skeleton, include int) match {
	var result match
	for field := dateField(0); field < fieldCount; field++ {
		want := 0
		if include&(1<<field) != 0 {
			want = requested.width(field)
		}
		have := candidate.width(field)
		switch {
		case want == have:
		case want == 0:
			result.distance += distanceExtra
			result.extra |= 1 << field
		case have == 0:
			result.distance += distanceMissing
			result.missing |= 1 << field
		default:
			difference := want - have
			if difference < 0 {
				difference = -difference
			}
			result.distance += difference
		}
	}
	return result
}

// bestRaw finds the available pattern closest to the requested fields in
// include. Ties go to the first tried.
func (locale *dateLocale) bestRaw(requested skeleton, include int) (available, match) {
	best := match{distance: math.MaxInt}
	var chosen available
	for _, candidate := range locale.available {
		result := distance(requested, candidate.skeleton, include)
		if result.distance < best.distance {
			best, chosen = result, candidate
			if result.distance == 0 {
				break
			}
		}
	}
	return chosen, best
}

// adjust fits a matched pattern's fields to the requested widths.
func adjust(tokens []patternToken, requested, matched skeleton, fixFraction bool, decimal string) []patternToken {
	out := make([]patternToken, 0, len(tokens)+2)
	for _, token := range tokens {
		if token.char == 0 {
			out = append(out, token)
			continue
		}
		field, _ := fieldOf(token.char)
		if fixFraction && field == fieldSecond {
			out = append(out, token, patternToken{literal: decimal}, patternToken{char: 'S', size: requested.size[fieldFraction]})
			continue
		}
		if !requested.has(field) {
			out = append(out, token)
			continue
		}
		wantChar, wantSize := requested.char[field], requested.size[field]
		if wantChar == 'E' && wantSize < 3 {
			wantSize = 3
		}
		size := wantSize
		switch {
		case field == fieldMinute || field == fieldSecond:
			size = token.size // lengths of these are never matched
		default:
			patternNumeric := widthOf(token.char, token.size) > 0
			skeletonNumeric := matched.width(field) > 0
			if matched.size[field] == wantSize || patternNumeric != skeletonNumeric {
				size = token.size
			}
		}
		char := token.char
		switch field {
		case fieldHour:
			if wantChar == 'h' {
				char = 'h'
			} else if wantChar == 'K' {
				char = 'h'
			}
		case fieldMonth, fieldWeekday, fieldYear:
		default:
			char = wantChar
		}
		if char == 'E' && size < 3 {
			char = 'e'
		}
		out = append(out, patternToken{char: char, size: size})
	}
	return out
}

// bestAppending builds a pattern for the fields in include, appending any
// field no pattern covers with the locale's append items.
func (locale *dateLocale) bestAppending(requested skeleton, include int) []patternToken {
	if include == 0 {
		return nil
	}
	chosen, result := locale.bestRaw(requested, include)
	tokens := adjust(chosen.tokens, requested, chosen.skeleton, false, locale.decimal)
	const secondsAndFraction = 1<<fieldSecond | 1<<fieldFraction
	if result.missing&secondsAndFraction == 1<<fieldFraction && include&secondsAndFraction == secondsAndFraction {
		// Seconds matched and fractional seconds did not: they join the
		// seconds field, as "s.SSS".
		tokens = adjust(tokens, requested, chosen.skeleton, true, locale.decimal)
		result.missing &^= 1 << fieldFraction
	}
	if result.missing == 0 {
		return tokens
	}
	// A field no pattern covers is appended with the locale's append item.
	// ICU applies one such item and loses any further missing field, so a
	// request with two gaps shows one of them; Node prints exactly that.
	next, nextResult := locale.bestRaw(requested, result.missing)
	addition := adjust(next.tokens, requested, next.skeleton, false, locale.decimal)
	covered := result.missing &^ nextResult.missing
	top := dateField(0)
	for field := dateField(0); field < fieldCount; field++ {
		if covered&(1<<field) != 0 {
			top = field
		}
	}
	return applyTemplate(locale.appendItems[top], tokens, addition, locale.fieldNames[top])
}

// applyTemplate fills a "{0} … {1} … {2}" template with two patterns and a
// literal.
func applyTemplate(template string, first, second []patternToken, name string) []patternToken {
	var out []patternToken
	for _, token := range parsePattern(template) {
		if token.char != 0 {
			out = append(out, token)
			continue
		}
		text := token.literal
		for text != "" {
			start := strings.IndexByte(text, '{')
			if start < 0 || start+2 >= len(text) || text[start+2] != '}' {
				out = appendLiteral(out, text)
				break
			}
			out = appendLiteral(out, text[:start])
			switch text[start+1] {
			case '0':
				out = appendTokens(out, first)
			case '1':
				out = appendTokens(out, second)
			case '2':
				out = appendLiteral(out, name)
			}
			text = text[start+3:]
		}
	}
	return out
}

func appendLiteral(tokens []patternToken, text string) []patternToken {
	if text == "" {
		return tokens
	}
	if count := len(tokens); count > 0 && tokens[count-1].char == 0 {
		tokens[count-1].literal += text
		return tokens
	}
	return append(tokens, patternToken{literal: text})
}

func appendTokens(tokens, more []patternToken) []patternToken {
	for _, token := range more {
		if token.char == 0 {
			tokens = appendLiteral(tokens, token.literal)
		} else {
			tokens = append(tokens, token)
		}
	}
	return tokens
}

// bestPattern is the pattern generator: the skeleton's closest pattern, or a
// date and a time pattern joined.
func (locale *dateLocale) bestPattern(requested skeleton) []patternToken {
	requested = requested.forMatching()
	chosen, result := locale.bestRaw(requested, -1)
	if result.missing == 0 && result.extra == 0 {
		return adjust(chosen.tokens, requested, chosen.skeleton, false, locale.decimal)
	}
	fields := requested.mask()
	date := locale.bestAppending(requested, fields&dateMask)
	clock := locale.bestAppending(requested, fields&^dateMask)
	switch {
	case len(date) == 0:
		return clock
	case len(clock) == 0:
		return date
	}
	style := 3
	switch requested.size[fieldMonth] {
	case 4:
		style = 1
		if requested.has(fieldWeekday) {
			style = 0
		}
	case 3:
		style = 2
	}
	return applyTemplate(locale.dateTimeStyles[style], clock, date, "")
}

// withHourCycle rewrites every hour field for an hour cycle, as V8 does after
// the generator.
func withHourCycle(tokens []patternToken, cycle string) []patternToken {
	char := map[string]byte{"h11": 'K', "h12": 'h', "h23": 'H', "h24": 'k'}[cycle]
	if char == 0 {
		return tokens
	}
	out := make([]patternToken, len(tokens))
	for index, token := range tokens {
		if field, ok := fieldOf(token.char); ok && field == fieldHour {
			token.char = char
		}
		out[index] = token
	}
	return out
}

// dateTimeOptions are the options a DateTimeFormat was created with, after
// the module has read and validated each one.
type dateTimeOptions struct {
	components            map[string]string
	fractionalDigits      int
	hour12                *bool
	hourCycle             string
	dateStyle, timeStyle  string
	timeZone, defaultZone string
	calendar, numbering   string
	required, defaults    string
}

var componentOrder = []string{"weekday", "era", "year", "month", "day", "dayPeriod", "hour", "minute", "second", "fractionalSecondDigits", "timeZoneName"}

func readDateTimeOptions(args []any) (dateTimeOptions, error) {
	raw := argMap(args, 1)
	options := dateTimeOptions{components: map[string]string{}}
	text := func(key string) string {
		value, _ := raw[key].(string)
		return value
	}
	for _, key := range componentOrder {
		if key == "fractionalSecondDigits" {
			if digits, ok := raw[key]; ok && digits != nil {
				count, err := argInt([]any{digits}, 0, "fractionalSecondDigits", 1, 3)
				if err != nil {
					return options, err
				}
				options.fractionalDigits = int(count)
			}
			continue
		}
		if value := text(key); value != "" {
			options.components[key] = value
		}
	}
	if value, ok := raw["hour12"].(bool); ok {
		options.hour12 = &value
	}
	options.hourCycle = text("hourCycle")
	options.dateStyle, options.timeStyle = text("dateStyle"), text("timeStyle")
	options.timeZone, options.defaultZone = text("timeZone"), text("defaultZone")
	options.calendar, options.numbering = text("calendar"), text("numberingSystem")
	options.required, options.defaults = text("required"), text("defaults")
	return options, nil
}

// resolveDateLocale picks the locale a date is formatted in. Only en-US data
// is shipped, so the first requested locale must be English as spoken in
// the United States; anything else is refused by name rather than answered in
// English.
func resolveDateLocale(requested []string) (tag language.Tag, name string, err error) {
	if len(requested) == 0 {
		return language.AmericanEnglish, "en-US", nil
	}
	first := requested[0]
	tag, err = parseLocale(first)
	if err != nil {
		return tag, "", err
	}
	base, script, region := tag.Raw()
	if base.String() != "en" || (script.String() != "Zzzz" && script.String() != "Latn") || (region.String() != "ZZ" && region.String() != "US") {
		return tag, "", rangeError("date formatting in locale %s is not supported", first)
	}
	name = "en"
	if region.String() == "US" {
		name = "en-US"
	}
	return tag, name, nil
}

func nativeDateTimeFormat(args []any) (any, error) {
	requested, err := canonicalLocales(argument(args, 0))
	if err != nil {
		return nil, err
	}
	options, err := readDateTimeOptions(args)
	if err != nil {
		return nil, err
	}
	tag, localeName, err := resolveDateLocale(requested)
	if err != nil {
		return nil, err
	}
	locale := enUS()

	// The calendar and numbering system: only gregory and latn are shipped.
	for _, pair := range [][3]string{{"calendar", options.calendar, "gregory"}, {"numberingSystem", options.numbering, "latn"}} {
		if pair[1] != "" && pair[1] != pair[2] {
			return nil, rangeError("the %s %s is not supported; only %s is", pair[0], pair[1], pair[2])
		}
	}
	for key, want := range map[string]string{"ca": "gregory", "nu": "latn"} {
		if value := unicodeExtension(tag, key); value != "" && value != want {
			return nil, rangeError("the locale %s asks for %s, which is not supported", requested[0], value)
		}
	}

	zoneName := options.timeZone
	if zoneName == "" {
		zoneName = options.defaultZone
	}
	if zoneName == "" {
		zoneName = "UTC"
	}
	zone, err := resolveZone(zoneName)
	if err != nil {
		return nil, err
	}

	// The hour cycle: hour12 wins, then hourCycle, then the locale's -u-hc,
	// then the locale's own (h12 for en-US).
	cycle := options.hourCycle
	extensionCycle := unicodeExtension(tag, "hc")
	switch {
	case options.hour12 != nil && *options.hour12:
		cycle = "h12"
	case options.hour12 != nil:
		cycle = "h23"
	case cycle == "" && extensionCycle != "":
		cycle = extensionCycle
	case cycle == "":
		cycle = "h12"
	}
	if extensionCycle != "" && options.hour12 == nil && (options.hourCycle == "" || options.hourCycle == extensionCycle) {
		localeName += "-u-hc-" + extensionCycle
	}

	var tokens []patternToken
	if options.dateStyle != "" || options.timeStyle != "" {
		for _, key := range componentOrder {
			if _, set := options.components[key]; set || key == "fractionalSecondDigits" && options.fractionalDigits != 0 {
				return nil, typeError("Invalid option : option")
			}
		}
		if options.required == "date" && options.timeStyle != "" && options.dateStyle == "" {
			return nil, typeError("Invalid option : timeStyle")
		}
		if options.required == "time" && options.dateStyle != "" && options.timeStyle == "" {
			return nil, typeError("Invalid option : dateStyle")
		}
		tokens = locale.stylePattern(options.dateStyle, options.timeStyle, cycle)
	} else {
		requestedSkeleton, err := optionsSkeleton(&options, cycle)
		if err != nil {
			return nil, err
		}
		tokens = locale.bestPattern(requestedSkeleton)
		if requestedSkeleton.has(fieldHour) {
			tokens = withHourCycle(tokens, cycle)
		}
	}

	resolved := map[string]any{
		"locale": localeName, "calendar": "gregory", "numberingSystem": "latn", "timeZone": zone.canonical,
	}
	shown := skeletonOfTokens(tokens)
	if shown.has(fieldHour) {
		resolved["hourCycle"] = map[byte]string{'K': "h11", 'h': "h12", 'H': "h23", 'k': "h24"}[shown.char[fieldHour]]
		resolved["hour12"] = shown.char[fieldHour] == 'K' || shown.char[fieldHour] == 'h'
	}
	if options.dateStyle == "" && options.timeStyle == "" {
		for key, value := range resolvedComponents(shown) {
			resolved[key] = value
		}
	} else {
		if options.dateStyle != "" {
			resolved["dateStyle"] = options.dateStyle
		}
		if options.timeStyle != "" {
			resolved["timeStyle"] = options.timeStyle
		}
	}
	// The zone formats under the name it was given: Etc/GMT-0 reports UTC
	// but still prints GMT, as in Node.
	resolved["pattern"], resolved["zone"] = formatPattern(tokens), zoneName
	return resolved, nil
}

// optionsSkeleton turns component options into a skeleton, filling in the
// defaults ToDateTimeOptions asks for when none was given.
func optionsSkeleton(options *dateTimeOptions, cycle string) (skeleton, error) {
	components := options.components
	needDefaults := true
	if options.required == "date" || options.required == "any" {
		for _, key := range []string{"weekday", "year", "month", "day"} {
			if _, set := components[key]; set {
				needDefaults = false
			}
		}
	}
	if options.required == "time" || options.required == "any" {
		for _, key := range []string{"dayPeriod", "hour", "minute", "second"} {
			if _, set := components[key]; set {
				needDefaults = false
			}
		}
		if options.fractionalDigits != 0 {
			needDefaults = false
		}
	}
	if needDefaults && (options.defaults == "date" || options.defaults == "all") {
		components["year"], components["month"], components["day"] = "numeric", "numeric", "numeric"
	}
	if needDefaults && (options.defaults == "time" || options.defaults == "all") {
		components["hour"], components["minute"], components["second"] = "numeric", "numeric", "numeric"
	}
	var s skeleton
	widths := map[string]map[string]string{
		"weekday":      {"narrow": "EEEEE", "short": "EEE", "long": "EEEE"},
		"era":          {"narrow": "GGGGG", "short": "G", "long": "GGGG"},
		"year":         {"2-digit": "yy", "numeric": "y"},
		"month":        {"2-digit": "MM", "numeric": "M", "narrow": "MMMMM", "short": "MMM", "long": "MMMM"},
		"day":          {"2-digit": "dd", "numeric": "d"},
		"dayPeriod":    {"narrow": "BBBBB", "short": "B", "long": "BBBB"},
		"minute":       {"2-digit": "mm", "numeric": "m"},
		"second":       {"2-digit": "ss", "numeric": "s"},
		"timeZoneName": {"short": "z", "long": "zzzz", "shortOffset": "O", "longOffset": "OOOO"},
	}
	for _, key := range componentOrder {
		value, set := components[key]
		if !set {
			continue
		}
		if key == "hour" {
			char := map[string]byte{"h11": 'K', "h12": 'h', "h23": 'H', "h24": 'k'}[cycle]
			size := 1
			if value == "2-digit" {
				size = 2
			}
			s.set(char, size)
			continue
		}
		if key == "timeZoneName" && (value == "shortGeneric" || value == "longGeneric") {
			return s, rangeError("timeZoneName %s is not supported", value)
		}
		letters, ok := widths[key][value]
		if !ok {
			return s, rangeError("Value %s out of range for Intl.DateTimeFormat options property %s", value, key)
		}
		s.set(letters[0], len(letters))
	}
	if options.fractionalDigits != 0 {
		s.set('S', options.fractionalDigits)
	}
	return s, nil
}

// stylePattern is the pattern for dateStyle and timeStyle. An hour cycle
// other than the locale's rebuilds the time through the generator, as V8
// does, so h23 gives "HH:mm" and drops the AM/PM.
func (locale *dateLocale) stylePattern(dateStyle, timeStyle, cycle string) []patternToken {
	var date, clock []patternToken
	if dateStyle != "" {
		date = parsePattern(locale.dateStyles[styleIndex[dateStyle]])
	}
	if timeStyle != "" {
		clock = parsePattern(locale.timeStyles[styleIndex[timeStyle]])
		if cycle != "h12" {
			s := skeletonOfTokens(clock)
			s.char[fieldHour] = map[string]byte{"h11": 'K', "h23": 'H', "h24": 'k'}[cycle]
			s.char[fieldDayPeriod], s.size[fieldDayPeriod] = 0, 0
			clock = withHourCycle(locale.bestPattern(s), cycle)
		}
	}
	switch {
	case date == nil:
		return clock
	case clock == nil:
		return date
	}
	return applyTemplate(locale.dateTimeStyles[styleIndex[dateStyle]], clock, date, "")
}

// resolvedComponents reports the component options a pattern shows, which
// is what resolvedOptions() lists.
func resolvedComponents(s skeleton) map[string]any {
	out := map[string]any{}
	numeric := func(size int) string {
		if size == 2 {
			return "2-digit"
		}
		return "numeric"
	}
	text := func(size int) string {
		switch size {
		case 4:
			return "long"
		case 5:
			return "narrow"
		}
		return "short"
	}
	if s.has(fieldWeekday) {
		out["weekday"] = text(s.size[fieldWeekday])
	}
	if s.has(fieldEra) {
		out["era"] = text(s.size[fieldEra])
	}
	if s.has(fieldYear) {
		out["year"] = numeric(s.size[fieldYear])
	}
	if s.has(fieldMonth) {
		if s.size[fieldMonth] <= 2 {
			out["month"] = numeric(s.size[fieldMonth])
		} else {
			out["month"] = text(s.size[fieldMonth])
		}
	}
	if s.has(fieldDay) {
		out["day"] = numeric(s.size[fieldDay])
	}
	if s.char[fieldDayPeriod] == 'B' {
		out["dayPeriod"] = text(s.size[fieldDayPeriod])
	}
	if s.has(fieldHour) {
		out["hour"] = numeric(s.size[fieldHour])
	}
	if s.has(fieldMinute) {
		out["minute"] = numeric(s.size[fieldMinute])
	}
	if s.has(fieldSecond) {
		out["second"] = numeric(s.size[fieldSecond])
	}
	if s.has(fieldFraction) {
		out["fractionalSecondDigits"] = s.size[fieldFraction]
	}
	if s.has(fieldZone) {
		out["timeZoneName"] = map[string]string{"z": "short", "zzzz": "long", "O": "shortOffset", "OOOO": "longOffset", "v": "shortGeneric", "vvvv": "longGeneric"}[strings.Repeat(string(s.char[fieldZone]), s.size[fieldZone])]
	}
	return out
}

// maxTime is the largest distance from the epoch, in milliseconds, a
// JavaScript date may hold.
const maxTime = 8.64e15

func nativeFormatDate(args []any) (any, error) {
	pattern, err := argString(args, 0, "pattern")
	if err != nil {
		return nil, err
	}
	zoneName, err := argString(args, 1, "timeZone")
	if err != nil {
		return nil, err
	}
	var ms float64
	switch value := argument(args, 2).(type) {
	case int64:
		ms = float64(value)
	case float64:
		ms = value
	default:
		return nil, typeError("the time must be a number")
	}
	if math.IsNaN(ms) || math.Abs(ms) > maxTime {
		return nil, rangeError("Invalid time value")
	}
	zone, err := resolveZone(zoneName)
	if err != nil {
		return nil, err
	}
	asParts, _ := argument(args, 3).(bool)
	parts, err := enUS().render(parsePattern(pattern), time.UnixMilli(int64(ms)).In(zone.location), zone)
	if err != nil {
		return nil, err
	}
	if !asParts {
		// CLDR puts a narrow no-break space before AM and PM. Node keeps it in
		// formatToParts and prints a plain space from format().
		var text strings.Builder
		for index := 1; index < len(parts); index += 2 {
			text.WriteString(strings.ReplaceAll(parts[index].(string), " ", " "))
		}
		return text.String(), nil
	}
	return parts, nil
}

// render formats an instant with a pattern, as alternating part types and
// values.
func (locale *dateLocale) render(tokens []patternToken, at time.Time, zone *resolvedZone) ([]any, error) {
	parts := make([]any, 0, len(tokens)*2)
	add := func(kind, value string) {
		if kind == "literal" && len(parts) > 0 && parts[len(parts)-2] == "literal" {
			parts[len(parts)-1] = parts[len(parts)-1].(string) + value
			return
		}
		parts = append(parts, kind, value)
	}
	year := at.Year()
	eraYear, era := year, 1
	if year <= 0 {
		eraYear, era = 1-year, 0
	}
	pad := func(value, size int) string {
		text := strconv.Itoa(value)
		for len(text) < size {
			text = "0" + text
		}
		return text
	}
	for _, token := range tokens {
		size := token.size
		switch token.char {
		case 0:
			add("literal", token.literal)
		case 'G':
			names := locale.eras
			if size == 4 {
				names = locale.erasLong
			} else if size == 5 {
				names = locale.erasNarrow
			}
			add("era", names[era])
		case 'y':
			if size == 2 {
				add("year", pad(eraYear%100, 2))
			} else {
				add("year", pad(eraYear, size))
			}
		case 'M', 'L':
			month := int(at.Month())
			switch size {
			case 1, 2:
				add("month", pad(month, size))
			case 3:
				add("month", locale.monthsShort[month-1])
			case 4:
				add("month", locale.months[month-1])
			default:
				add("month", locale.monthsNarrow[month-1])
			}
		case 'd':
			add("day", pad(at.Day(), size))
		case 'E', 'c', 'e':
			day := int(at.Weekday())
			switch {
			case size <= 3:
				add("weekday", locale.weekdaysShort[day])
			case size == 4:
				add("weekday", locale.weekdays[day])
			case size == 5:
				add("weekday", locale.weekdaysNarrow[day])
			default:
				add("weekday", locale.weekdaysShorter[day])
			}
		case 'a':
			add("dayPeriod", locale.amPM[at.Hour()/12])
		case 'B':
			shown := skeletonOfTokens(tokens)
			add("dayPeriod", locale.flexibleDayPeriod(at, size, shown.has(fieldMinute), shown.has(fieldSecond)))
		case 'h':
			hour := at.Hour() % 12
			if hour == 0 {
				hour = 12
			}
			add("hour", pad(hour, size))
		case 'H':
			add("hour", pad(at.Hour(), size))
		case 'K':
			add("hour", pad(at.Hour()%12, size))
		case 'k':
			hour := at.Hour()
			if hour == 0 {
				hour = 24
			}
			add("hour", pad(hour, size))
		case 'm':
			add("minute", pad(at.Minute(), size))
		case 's':
			add("second", pad(at.Second(), size))
		case 'S':
			digits := pad(at.Nanosecond()/int(time.Millisecond), 3)
			for len(digits) < size {
				digits += "0"
			}
			add("fractionalSecond", digits[:size])
		case 'z', 'O', 'v':
			style := map[string]string{"z": "short", "zzzz": "long", "O": "shortOffset", "OOOO": "longOffset"}[strings.Repeat(string(token.char), size)]
			if style == "" {
				style = "short"
			}
			name, err := zoneName(at, zone, style)
			if err != nil {
				return nil, err
			}
			add("timeZoneName", name)
		default:
			return nil, rangeError("the date field %q is not supported", string(token.char))
		}
	}
	return parts, nil
}

// flexibleDayPeriod names the part of the day an instant falls in: "in the
// morning", "noon" and so on. Noon is noon at the precision the pattern
// shows: the whole twelve o'clock hour when it shows no minutes, and only
// 12:00 when it does.
func (locale *dateLocale) flexibleDayPeriod(at time.Time, size int, minutes, seconds bool) string {
	hour := at.Hour()
	if hour == 12 && (!minutes || at.Minute() == 0) && (!seconds || at.Second() == 0) {
		if size == 5 {
			return locale.noonNarrow
		}
		return locale.noon
	}
	for _, period := range locale.dayPeriods {
		if hour*60+at.Minute() >= period.from && hour*60+at.Minute() < period.to {
			return period.name
		}
	}
	return ""
}

// ---- Numbers ------------------------------------------------------------------------

// numberSymbols are what a locale writes a number with. They are read from
// x/text's CLDR data by formatting a known number and taking it apart, since
// x/text formats but does not expose its symbols, and then corrected where
// CLDR has changed since the copy x/text carries (numberCorrections).
type numberSymbols struct {
	locale                       string
	decimal, group, minus, plus  string
	percentPrefix, percentSuffix string
	nan, infinity                string
	zero                         rune
	numbering                    string
	primary, secondary           int
	minimumGrouping              int
}

// numberingSystems names the numbering systems by their zero digit.
var numberingSystems = map[rune]string{
	'0': "latn", '٠': "arab", '۰': "arabext", '०': "deva", '০': "beng", '੦': "guru",
	'૦': "gujr", '୦': "orya", '௦': "tamldec", '౦': "telu", '೦': "knda", '൦': "mlym",
	'๐': "thai", '໐': "laoo", '༠': "tibt", '၀': "mymr", '០': "khmr", '᠐': "mong",
}

var symbolCache = struct {
	sync.Mutex
	entries map[string]*numberSymbols
}{entries: map[string]*numberSymbols{}}

// symbolsFor reads a locale's number symbols, caching up to a few hundred
// locales.
func symbolsFor(tag language.Tag) *numberSymbols {
	key := tag.String()
	symbolCache.Lock()
	cached := symbolCache.entries[key]
	symbolCache.Unlock()
	if cached != nil {
		return cached
	}
	symbols := readSymbols(tag)
	symbolCache.Lock()
	if len(symbolCache.entries) >= 512 {
		symbolCache.entries = map[string]*numberSymbols{}
	}
	symbolCache.entries[key] = symbols
	symbolCache.Unlock()
	return symbols
}

func readSymbols(tag language.Tag) *numberSymbols {
	base, _, region := tag.Raw()
	lang, territory := base.String(), region.String()
	source := tag
	switch {
	case lang == "ar" && strings.Contains(arabicDigitRegions, territory):
		source = languageTag("ar") // x/text's own Arabic writes Arabic-Indic digits
	case lang == "ar" && territory == "ZZ":
		source = languageTag("ar-AE") // CLDR 48's Arabic writes Latin digits
	case lang == "bn" || lang == "fa" || lang == "ne":
		source = languageTag(lang) // every region keeps the language's digits
	}
	symbols := rawSymbols(source)
	symbols.locale = tag.String()
	correctSymbols(lang, territory, symbols)
	return symbols
}

func languageTag(name string) language.Tag {
	tag, _ := language.Parse(name)
	return tag
}

// rawSymbols reads what x/text's CLDR data says about a locale.
func rawSymbols(tag language.Tag) *numberSymbols {
	printer := message.NewPrinter(tag)
	symbols := &numberSymbols{plus: "+", minimumGrouping: 1}

	// -1234567890.5 shows the minus sign, the digits, the group separator
	// and both grouping sizes, and the decimal separator.
	sample := []rune(printer.Sprint(number.Decimal(-1234567890.5)))
	first := -1
	var runs []string
	var separators []string
	current, between := "", ""
	for index, char := range sample {
		if unicode.IsDigit(char) {
			if first < 0 {
				first = index
				symbols.zero = char - 1
			}
			if between != "" && current == "" && len(runs) > 0 {
				separators = append(separators, between)
			}
			between = ""
			current += string(char)
			continue
		}
		if first < 0 {
			continue
		}
		if current != "" {
			runs = append(runs, current)
			current = ""
		}
		between += string(char)
	}
	if current != "" {
		runs = append(runs, current)
	}
	symbols.minus = string(sample[:max(first, 0)])
	if len(separators) > 0 {
		symbols.decimal = separators[len(separators)-1]
		separators = separators[:len(separators)-1]
		runs = runs[:len(runs)-1]
	}
	if len(separators) > 0 {
		symbols.group = separators[0]
		symbols.primary = utf8.RuneCountInString(runs[len(runs)-1])
		symbols.secondary = symbols.primary
		if len(runs) >= 3 {
			symbols.secondary = utf8.RuneCountInString(runs[len(runs)-2])
		}
	}
	symbols.numbering = numberingSystems[symbols.zero]

	// A percentage shows where the percent sign goes.
	percent := []rune(printer.Sprint(number.Percent(0.5)))
	start, end := -1, -1
	for index, char := range percent {
		if unicode.IsDigit(char) {
			if start < 0 {
				start = index
			}
			end = index + 1
		}
	}
	if start >= 0 {
		symbols.percentPrefix, symbols.percentSuffix = string(percent[:start]), string(percent[end:])
	}
	symbols.nan = printer.Sprint(number.Decimal(math.NaN()))
	symbols.infinity = strings.TrimLeft(printer.Sprint(number.Decimal(math.Inf(1))), "+")
	return symbols
}

// CLDR regions by the conventions that changed between x/text's copy and
// Node's.
const (
	// arabicDigitRegions write Arabic in Arabic-Indic digits.
	arabicDigitRegions = "BH DJ EG ER IL IQ JO KM KW LB MR OM PS QA SA SD SO SS SY TD YE"
	// latinAmericanSpanish follows es-419, not es-ES.
	latinAmericanSpanish = "419 AR BO BR BZ CL CO CR CU DO EC GT HN MX NI PA PE PR PY SV US UY VE"
)

// correctSymbols applies what CLDR 48, which Node 24 ships, changed since
// the CLDR 32 copy in golang.org/x/text. Each rule was found by the locale
// sweep in testdata/parity/numbers.json, which the tests hold it to.
func correctSymbols(lang, region string, symbols *numberSymbols) {
	percent := func(suffix string) { symbols.percentPrefix, symbols.percentSuffix = "", suffix }
	switch lang {
	case "fr":
		// French groups with a narrow no-break space, Switzerland with an
		// apostrophe; Canada keeps the no-break space.
		switch {
		case region == "CH":
			symbols.group = "'"
		case region != "CA" && symbols.group == " ":
			symbols.group = " "
		}
	case "de", "it", "en":
		if region == "CH" || region == "LI" {
			symbols.group, symbols.decimal = "'", "."
		}
		if lang == "en" && region == "150" {
			symbols.group, symbols.decimal = ",", "."
		}
	case "es":
		if region != "ZZ" && strings.Contains(latinAmericanSpanish, region) {
			percent("%")
		} else {
			symbols.minimumGrouping = 2
		}
	case "pt":
		if region != "ZZ" && region != "BR" && region != "AO" {
			symbols.minimumGrouping = 2
		}
	case "bs":
		percent("%")
	case "ca", "mk":
		percent(" %")
	case "hr":
		percent(" %")
		symbols.minus = "−"
	case "km":
		symbols.group, symbols.decimal = ",", "."
	case "ne", "te":
		symbols.primary, symbols.secondary = 3, 2
	}
	switch lang {
	case "be", "bg", "et", "hu", "hy", "it", "ka", "lv", "pl", "sl", "sq":
		symbols.minimumGrouping = 2
	}
}

// numberOptions are a NumberFormat's resolved options.
type numberOptions struct {
	locale                            string
	tag                               language.Tag
	style                             string
	currency, currencyDisplay         string
	currencySign                      string
	minimumIntegerDigits              int
	minimumFractionDigits             int
	maximumFractionDigits             int
	minimumSignificantDigits          int
	maximumSignificantDigits          int
	significant                       bool
	useGrouping                       string // "auto", "always", "min2" or "" for none
	signDisplay                       string
	roundingMode, trailingZeroDisplay string
}

func optionString(raw map[string]any, key, fallback string) string {
	if value, ok := raw[key].(string); ok && value != "" {
		return value
	}
	return fallback
}

func optionInt(raw map[string]any, key string) (int, bool) {
	switch value := raw[key].(type) {
	case int64:
		return int(value), true
	case float64:
		return int(value), true
	}
	return 0, false
}

// resolveNumberLocale picks the locale numbers are formatted in: the first
// requested one, or en-US.
func resolveNumberLocale(requested []string) (language.Tag, string, error) {
	if len(requested) == 0 {
		return language.AmericanEnglish, "en-US", nil
	}
	tag, err := parseLocale(requested[0])
	if err != nil {
		return tag, "", err
	}
	if numbering := unicodeExtension(tag, "nu"); numbering != "" {
		symbols := symbolsFor(tag)
		if numbering != symbols.numbering {
			return tag, "", rangeError("the numbering system %s is not supported; locale %s uses %s", numbering, requested[0], symbols.numbering)
		}
	}
	base, script, region := tag.Raw()
	name := base.String()
	if script.String() != "Zzzz" && strings.Contains(requested[0], "-"+script.String()) {
		name += "-" + script.String()
	}
	if region.String() != "ZZ" {
		name += "-" + region.String()
	}
	if name == "und" {
		return language.AmericanEnglish, "en-US", nil
	}
	return tag, name, nil
}

func readNumberOptions(args []any) (numberOptions, error) {
	requested, err := canonicalLocales(argument(args, 0))
	if err != nil {
		return numberOptions{}, err
	}
	raw := argMap(args, 1)
	tag, name, err := resolveNumberLocale(requested)
	if err != nil {
		return numberOptions{}, err
	}
	options := numberOptions{locale: name, tag: tag}
	if numbering := optionString(raw, "numberingSystem", ""); numbering != "" && numbering != symbolsFor(tag).numbering {
		return options, rangeError("the numbering system %s is not supported; locale %s uses %s", numbering, name, symbolsFor(tag).numbering)
	}
	options.style = optionString(raw, "style", "decimal")
	switch options.style {
	case "decimal", "percent":
	case "currency":
		code := optionString(raw, "currency", "")
		if code == "" {
			return options, typeError("Currency code is required with currency style.")
		}
		if len(code) != 3 || strings.IndexFunc(code, func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') }) >= 0 {
			return options, rangeError("Invalid currency code : %s", code)
		}
		options.currency = strings.ToUpper(code)
		if _, err := currency.ParseISO(options.currency); err == nil && !strings.Contains(verifiedCurrencies, options.currency) {
			return options, rangeError("the currency %s is not supported", options.currency)
		}
		options.currencyDisplay = optionString(raw, "currencyDisplay", "symbol")
		if options.currencyDisplay == "name" {
			return options, rangeError("currencyDisplay name is not supported; use symbol, narrowSymbol or code")
		}
		options.currencySign = optionString(raw, "currencySign", "standard")
		if options.currencySign != "standard" {
			return options, rangeError("currencySign %s is not supported", options.currencySign)
		}
	case "unit":
		return options, rangeError("the unit style of Intl.NumberFormat is not supported")
	default:
		return options, rangeError("Value %s out of range for Intl.NumberFormat options property style", options.style)
	}
	if notation := optionString(raw, "notation", "standard"); notation != "standard" {
		return options, rangeError("notation %s is not supported", notation)
	}
	if increment, ok := optionInt(raw, "roundingIncrement"); ok && increment != 1 {
		return options, rangeError("roundingIncrement %d is not supported", increment)
	}
	if priority := optionString(raw, "roundingPriority", "auto"); priority != "auto" {
		return options, rangeError("roundingPriority %s is not supported", priority)
	}
	options.roundingMode = optionString(raw, "roundingMode", "halfExpand")
	options.trailingZeroDisplay = optionString(raw, "trailingZeroDisplay", "auto")
	options.signDisplay = optionString(raw, "signDisplay", "auto")

	// The digit options (SetNumberFormatDigitOptions).
	minimumFractionDefault, maximumFractionDefault := 0, 3
	switch options.style {
	case "currency":
		digits := currencyDigits(options.currency)
		minimumFractionDefault, maximumFractionDefault = digits, digits
	case "percent":
		minimumFractionDefault, maximumFractionDefault = 0, 0
	}
	options.minimumIntegerDigits = 1
	if value, ok := optionInt(raw, "minimumIntegerDigits"); ok {
		options.minimumIntegerDigits = value
	}
	minimumSignificant, hasMinimumSignificant := optionInt(raw, "minimumSignificantDigits")
	maximumSignificant, hasMaximumSignificant := optionInt(raw, "maximumSignificantDigits")
	minimumFraction, hasMinimumFraction := optionInt(raw, "minimumFractionDigits")
	maximumFraction, hasMaximumFraction := optionInt(raw, "maximumFractionDigits")
	if hasMinimumSignificant || hasMaximumSignificant {
		options.significant = true
		options.minimumSignificantDigits, options.maximumSignificantDigits = 1, 21
		if hasMinimumSignificant {
			options.minimumSignificantDigits = minimumSignificant
		}
		if hasMaximumSignificant {
			options.maximumSignificantDigits = maximumSignificant
		}
		if options.minimumSignificantDigits > options.maximumSignificantDigits {
			return options, rangeError("maximumSignificantDigits value is out of range.")
		}
		// resolvedOptions reports the fraction digits too, at their defaults.
		options.minimumFractionDigits, options.maximumFractionDigits = minimumFractionDefault, maximumFractionDefault
	} else if hasMinimumFraction || hasMaximumFraction {
		switch {
		case !hasMinimumFraction:
			minimumFraction = min(minimumFractionDefault, maximumFraction)
		case !hasMaximumFraction:
			maximumFraction = max(maximumFractionDefault, minimumFraction)
		case minimumFraction > maximumFraction:
			return options, rangeError("maximumFractionDigits value is out of range.")
		}
		options.minimumFractionDigits, options.maximumFractionDigits = minimumFraction, maximumFraction
	} else {
		options.minimumFractionDigits, options.maximumFractionDigits = minimumFractionDefault, maximumFractionDefault
	}
	switch grouping := raw["useGrouping"].(type) {
	case nil:
		options.useGrouping = "auto"
	case bool:
		if grouping {
			options.useGrouping = "always"
		}
	case string:
		options.useGrouping = grouping
	}
	return options, nil
}

// currencyDigits is how many fraction digits a currency shows by default.
// x/text's CLDR copy is corrected where ICU now differs.
func currencyDigits(code string) int {
	if digits, ok := currencyDigitCorrections[code]; ok {
		return digits
	}
	unit, err := currency.ParseISO(code)
	if err != nil {
		return 2
	}
	scale, _ := currency.Standard.Rounding(unit)
	return scale
}

func (options numberOptions) resolved() map[string]any {
	out := map[string]any{
		"locale": options.locale, "numberingSystem": symbolsFor(options.tag).numbering, "style": options.style,
		"minimumIntegerDigits": options.minimumIntegerDigits,
		"notation":             "standard", "signDisplay": options.signDisplay,
		"roundingIncrement": 1, "roundingMode": options.roundingMode, "roundingPriority": "auto",
		"trailingZeroDisplay": options.trailingZeroDisplay,
	}
	if options.useGrouping == "" {
		out["useGrouping"] = false
	} else {
		out["useGrouping"] = options.useGrouping
	}
	// Significant digits replace the fraction digits in what is reported.
	if options.significant {
		out["minimumSignificantDigits"] = options.minimumSignificantDigits
		out["maximumSignificantDigits"] = options.maximumSignificantDigits
	} else {
		out["minimumFractionDigits"] = options.minimumFractionDigits
		out["maximumFractionDigits"] = options.maximumFractionDigits
	}
	if options.style == "currency" {
		out["currency"] = options.currency
		out["currencyDisplay"] = options.currencyDisplay
		out["currencySign"] = options.currencySign
	}
	return out
}

func nativeNumberFormat(args []any) (any, error) {
	options, err := readNumberOptions(args)
	if err != nil {
		return nil, err
	}
	if options.style == "currency" {
		if _, err := currencyAffixes(options); err != nil {
			return nil, err
		}
	}
	return options.resolved(), nil
}

// decimal is an exact decimal number: 0.digits × 10^exponent.
type decimal struct {
	negative bool
	digits   []byte // '0'…'9', no leading or trailing zeros; empty is zero
	exponent int
	nan, inf bool
}

func decimalOfFloat(value float64) decimal {
	switch {
	case math.IsNaN(value):
		return decimal{nan: true}
	case math.IsInf(value, 0):
		return decimal{inf: true, negative: value < 0}
	}
	number := decimal{negative: math.Signbit(value)}
	if value == 0 {
		return number
	}
	text := strconv.FormatFloat(math.Abs(value), 'e', -1, 64)
	mantissa, exponent, _ := strings.Cut(text, "e")
	power, _ := strconv.Atoi(exponent)
	number.digits = []byte(strings.Replace(mantissa, ".", "", 1))
	number.exponent = power + 1
	number.trim()
	return number
}

// decimalLiteral is a StringNumericLiteral in decimal form.
var decimalLiteral = regexp.MustCompile(`^([+-]?)(?:(\d+)(?:\.(\d*))?|\.(\d+))(?:[eE]([+-]?\d+))?$`)

// decimalOfString reads a string as ECMA-402 reads it: exactly, when it is a
// finite decimal a double could hold without overflowing to infinity or
// underflowing to zero, which is where Node's own reading of it lands.
func decimalOfString(text string) decimal {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) > 1000 {
		return decimal{nan: true}
	}
	if trimmed == "" {
		return decimal{}
	}
	switch strings.TrimLeft(trimmed, "+") {
	case "Infinity":
		return decimal{inf: true}
	case "-Infinity":
		return decimal{inf: true, negative: true}
	}
	if len(trimmed) > 2 && trimmed[0] == '0' && strings.ContainsRune("xXoObB", rune(trimmed[1])) {
		value, ok := new(big.Int).SetString(trimmed, 0)
		if !ok {
			return decimal{nan: true}
		}
		return decimalOfString(value.String())
	}
	match := decimalLiteral.FindStringSubmatch(trimmed)
	if match == nil {
		return decimal{nan: true}
	}
	approximate, _ := strconv.ParseFloat(trimmed, 64)
	if math.IsInf(approximate, 0) || approximate == 0 {
		return decimalOfFloat(approximate)
	}
	whole, fraction := match[2], match[3]
	if match[4] != "" {
		fraction = match[4]
	}
	power := 0
	if match[5] != "" {
		power, _ = strconv.Atoi(match[5])
	}
	number := decimal{negative: match[1] == "-", digits: []byte(whole + fraction), exponent: len(whole) + power}
	number.trim()
	return number
}

func (d *decimal) trim() {
	leading := 0
	for leading < len(d.digits) && d.digits[leading] == '0' {
		leading++
	}
	d.digits = d.digits[leading:]
	d.exponent -= leading
	end := len(d.digits)
	for end > 0 && d.digits[end-1] == '0' {
		end--
	}
	d.digits = d.digits[:end]
	if len(d.digits) == 0 {
		d.exponent = 0
	}
}

func (d decimal) isZero() bool { return len(d.digits) == 0 }

// round keeps keep digits (counted from the start of digits; it may be zero
// or negative), rounding the rest away by mode.
func (d *decimal) round(keep int, mode string) {
	if keep >= len(d.digits) {
		return
	}
	var dropped []byte
	if keep <= 0 {
		// Every digit goes; a carry lands one place above the rounding
		// position, 10^(exponent-keep).
		dropped = append([]byte(strings.Repeat("0", -keep)), d.digits...)
		d.digits = nil
		d.exponent -= keep
	} else {
		dropped = d.digits[keep:]
		d.digits = append([]byte(nil), d.digits[:keep]...)
	}
	// half compares the dropped digits with one half: -1 below, 0 exactly,
	// 1 above.
	half := 0
	switch {
	case dropped[0] > '5':
		half = 1
	case dropped[0] < '5':
		half = -1
	default:
		for _, digit := range dropped[1:] {
			if digit != '0' {
				half = 1
				break
			}
		}
	}
	nonzero := false
	for _, digit := range dropped {
		if digit != '0' {
			nonzero = true
			break
		}
	}
	lastOdd := len(d.digits) > 0 && (d.digits[len(d.digits)-1]-'0')%2 == 1
	up := false
	switch mode {
	case "ceil":
		up = nonzero && !d.negative
	case "floor":
		up = nonzero && d.negative
	case "expand":
		up = nonzero
	case "trunc":
		up = false
	case "halfCeil":
		up = half > 0 || half == 0 && !d.negative
	case "halfFloor":
		up = half > 0 || half == 0 && d.negative
	case "halfTrunc":
		up = half > 0
	case "halfEven":
		up = half > 0 || half == 0 && lastOdd
	default: // halfExpand
		up = half >= 0
	}
	if up {
		carry := true
		for index := len(d.digits) - 1; index >= 0 && carry; index-- {
			if d.digits[index] == '9' {
				d.digits[index] = '0'
			} else {
				d.digits[index]++
				carry = false
			}
		}
		if carry {
			d.digits = append([]byte{'1'}, d.digits...)
			d.exponent++
		}
	}
	d.trim()
}

// numberParts renders a number as alternating part types and values.
func (options numberOptions) numberParts(value decimal) ([]any, error) {
	symbols := symbolsFor(options.tag)
	var parts []any
	add := func(kind, text string) {
		if text == "" {
			return
		}
		if kind == "literal" && len(parts) > 0 && parts[len(parts)-2] == "literal" {
			parts[len(parts)-1] = parts[len(parts)-1].(string) + text
			return
		}
		parts = append(parts, kind, text)
	}

	if value.nan {
		value.negative = false
	}
	if options.style == "percent" && !value.isZero() {
		value.exponent += 2
	}
	if !value.nan && !value.inf {
		if options.significant {
			value.round(options.maximumSignificantDigits, options.roundingMode)
		} else {
			value.round(value.exponent+options.maximumFractionDigits, options.roundingMode)
		}
	}

	// The sign, by signDisplay, after rounding.
	sign := ""
	zero := value.isZero() && !value.nan && !value.inf
	switch options.signDisplay {
	case "auto":
		if value.negative {
			sign = "-"
		}
	case "always":
		sign = "+"
		if value.negative {
			sign = "-"
		}
	case "exceptZero":
		if !zero && !value.nan {
			sign = "+"
			if value.negative {
				sign = "-"
			}
		}
	case "negative":
		if value.negative && !zero {
			sign = "-"
		}
	}

	// The digits.
	var integer, fraction string
	if !value.nan && !value.inf {
		integer, fraction = options.digitStrings(value)
	}

	group, decimalSeparator := symbols.group, symbols.decimal
	var money currencyAffix
	if options.style == "currency" {
		var err error
		if money, err = currencyAffixes(options); err != nil {
			return nil, err
		}
		if money.form.group != "" {
			group = money.form.group
		}
		if money.form.decimal != "" {
			decimalSeparator = money.form.decimal
		}
	}

	addSign := func() {
		switch sign {
		case "-":
			add("minusSign", symbols.minus)
		case "+":
			add("plusSign", symbols.plus)
		}
	}
	addAffix := func(affix string) {
		for _, sign := range []string{"%", "٪"} {
			if index := strings.Index(affix, sign); index >= 0 {
				add("literal", affix[:index])
				add("percentSign", sign)
				add("literal", affix[index+len(sign):])
				return
			}
		}
		add("literal", affix)
	}
	addNumber := func() {
		switch {
		case value.nan:
			add("nan", symbols.nan)
		case value.inf:
			add("infinity", symbols.infinity)
		default:
			for index, digits := range options.group(integer, symbols) {
				if index > 0 {
					add("group", group)
				}
				add("integer", options.localDigits(digits, symbols))
			}
			if fraction != "" {
				add("decimal", decimalSeparator)
				add("fraction", options.localDigits(fraction, symbols))
			}
		}
	}

	switch {
	case options.style == "percent":
		addSign()
		addAffix(symbols.percentPrefix)
		addNumber()
		addAffix(symbols.percentSuffix)
	case options.style == "currency" && money.form.before:
		switch {
		case money.form.negative == "inside": // "US$ -1.234,50"
			add("currency", money.symbol)
			add("literal", money.space)
			addSign()
		case money.form.negative == "attached" && sign != "": // "$-1'234.50"
			add("currency", money.symbol)
			addSign()
		default:
			addSign()
			add("currency", money.symbol)
			add("literal", money.space)
		}
		addNumber()
	case options.style == "currency":
		addSign()
		addNumber()
		add("literal", money.space)
		add("currency", money.symbol)
	default:
		addSign()
		addNumber()
	}
	return parts, nil
}

// digitStrings writes a rounded number's integer and fraction digits,
// padded to the minimums and with trailing zeros past them removed.
func (options numberOptions) digitStrings(value decimal) (string, string) {
	digits, exponent := string(value.digits), value.exponent
	if options.significant {
		if value.isZero() {
			digits, exponent = "0", 1 // zero shows one significant digit
		}
		if len(digits) < options.minimumSignificantDigits {
			digits += strings.Repeat("0", options.minimumSignificantDigits-len(digits))
		}
	}
	integer, fraction := "0", ""
	if exponent > 0 {
		if len(digits) < exponent {
			digits += strings.Repeat("0", exponent-len(digits))
		}
		integer, fraction = digits[:exponent], digits[exponent:]
	} else if digits != "" {
		fraction = strings.Repeat("0", -exponent) + digits
	}
	if !options.significant && len(fraction) < options.minimumFractionDigits {
		fraction += strings.Repeat("0", options.minimumFractionDigits-len(fraction))
	}
	if options.trailingZeroDisplay == "stripIfInteger" && strings.Trim(fraction, "0") == "" {
		fraction = ""
	}
	for len(integer) < options.minimumIntegerDigits {
		integer = "0" + integer
	}
	return integer, fraction
}

// group splits integer digits by the locale's grouping sizes.
func (options numberOptions) group(integer string, symbols *numberSymbols) []string {
	minimum := symbols.minimumGrouping
	switch options.useGrouping {
	case "":
		return []string{integer}
	case "always":
		minimum = 1
	case "min2":
		minimum = 2
	}
	if symbols.primary == 0 || len(integer) < symbols.primary+minimum {
		return []string{integer}
	}
	var groups []string
	rest := integer
	size := symbols.primary
	for len(rest) > size {
		groups = append([]string{rest[len(rest)-size:]}, groups...)
		rest = rest[:len(rest)-size]
		size = symbols.secondary
	}
	return append([]string{rest}, groups...)
}

func (options numberOptions) localDigits(digits string, symbols *numberSymbols) string {
	if symbols.zero == '0' {
		return digits
	}
	var out strings.Builder
	for _, digit := range digits {
		out.WriteRune(symbols.zero + (digit - '0'))
	}
	return out.String()
}

// currencyForm is how a locale writes an amount of money: CLDR's standard
// currency pattern, reduced to where the currency goes, the space the pattern
// puts beside it, where a minus sign goes, and the separators, where the
// locale's currency separators differ from its number ones.
type currencyForm struct {
	before bool
	// space is the pattern's own space; when it has none, CLDR's currency
	// spacing decides (a letter beside a digit gets a no-break space).
	space string
	// negative is where a minus goes: before everything (""), after the
	// currency and its space ("inside"), or straight after the currency
	// ("attached").
	negative       string
	group, decimal string
}

var (
	currencyFirst       = currencyForm{before: true}
	currencyFirstSpaced = currencyForm{before: true, space: " "}
	currencyLast        = currencyForm{space: " "}
)

// currencyForms are the locales currency style formats in, each one held to
// Node by the currency sweep in testdata/parity/numbers.json. A locale must
// match an entry exactly, by its language alone or its language and region:
// a region not listed is refused rather than given its language's pattern,
// because regions differ (en-AU writes "USD 1.00" where en-US writes "$1.00").
var currencyForms = map[string]currencyForm{
	"en": currencyFirst, "en-US": currencyFirst, "en-GB": currencyFirst, "en-AU": currencyFirst, "en-CA": currencyFirst,
	"en-IN": currencyFirst, "en-SG": currencyFirst, "en-PH": currencyFirst,
	"de": currencyLast, "de-DE": currencyLast,
	"de-AT": {before: true, space: " ", group: "."},
	"de-CH": {before: true, space: " ", negative: "attached"},
	"fr":    currencyLast, "fr-FR": currencyLast, "fr-CA": currencyLast,
	"fr-CH": {space: " ", decimal: "."},
	"es":    currencyLast, "es-ES": currencyLast, "es-MX": currencyFirst, "es-AR": currencyFirstSpaced,
	"it": currencyLast, "it-IT": currencyLast,
	"pt": currencyFirstSpaced, "pt-BR": currencyFirstSpaced, "pt-PT": currencyLast,
	"nl": {before: true, space: " ", negative: "inside"}, "nl-NL": {before: true, space: " ", negative: "inside"},
	"nl-BE": {before: true, space: " ", negative: "inside"},
	"pl":    currencyLast, "ru": currencyLast, "sv": currencyLast, "da": currencyLast, "nb": currencyLast, "fi": currencyLast,
	"cs": currencyLast, "vi": currencyLast, "uk": currencyLast,
	"tr": currencyFirst, "id": currencyFirst, "id-ID": currencyFirst, "ms": currencyFirst, "ms-MY": currencyFirst,
	"ja": currencyFirst, "ja-JP": currencyFirst, "zh": currencyFirst, "zh-CN": currencyFirst, "zh-TW": currencyFirst,
	"zh-HK": currencyFirst, "ko": currencyFirst, "hi": currencyFirst, "th": currencyFirst, "fil": currencyFirst,
}

// verifiedCurrencies are the currencies Node 24 knows, each one's symbols
// held to Node in every locale of currencyForms by
// testdata/parity/currencies.json. A code outside the list that x/text
// knows, such as a withdrawn currency, has symbols no one has checked, so it
// is refused; a code neither knows is written as itself, as Node writes it.
const verifiedCurrencies = `AED AFN ALL AMD ANG AOA ARS AUD AWG AZN BAM BBD BDT BGN BHD BIF BMD BND BOB BRL BSD BTN BWP BYN BZD CAD CDF
CHF CLP CNY COP CRC CUC CUP CVE CZK DJF DKK DOP DZD EGP ERN ETB EUR FJD FKP GBP GEL GHS GIP GMD GNF GTQ GYD
HKD HNL HRK HTG HUF IDR ILS INR IQD IRR ISK JMD JOD JPY KES KGS KHR KMF KPW KRW KWD KYD KZT LAK LBP LKR LRD
LSL LYD MAD MDL MGA MKD MMK MNT MOP MRU MUR MVR MWK MXN MYR MZN NAD NGN NIO NOK NPR NZD OMR PAB PEN PGK PHP
PKR PLN PYG QAR RON RSD RUB RWF SAR SBD SCR SDG SEK SGD SHP SLE SLL SOS SRD SSP STN SVC SYP SZL THB TJS TMT
TND TOP TRY TTD TWD TZS UAH UGX USD UYU UZS VES VND VUV WST XAF XCD XCG XDR XOF XPF XSU YER ZAR ZMW ZWG ZWL`

// currencySymbolDefaults are symbols CLDR has changed since x/text's copy for
// every locale, keyed "display|code"; currencySymbolCorrections are the
// changes for one locale, keyed "locale|display|code", and win over both.
// Both were derived from the currency sweep, which the tests hold them to.
var currencySymbolDefaults = map[string]string{
	"narrowSymbol|AFN": "؋",
	"narrowSymbol|AMD": "֏",
	"narrowSymbol|AZN": "₼",
	"narrowSymbol|GHS": "GH₵",
	"narrowSymbol|KGS": "⃀",
	"narrowSymbol|STN": "Db",
	"narrowSymbol|XCG": "Cg.",
	"narrowSymbol|XOF": "F CFA",
	"symbol|BSD":       "BSD",
	"symbol|XCG":       "Cg.",
	"symbol|XOF":       "F CFA",
}

var currencySymbolCorrections = map[string]string{
	"da|narrowSymbol|BYN":    "Br.",
	"da|symbol|USD":          "US$",
	"de-AT|narrowSymbol|GHS": "₵",
	"de-CH|narrowSymbol|GHS": "₵",
	"de-DE|narrowSymbol|GHS": "₵",
	"de|narrowSymbol|GHS":    "₵",
	"en-AU|narrowSymbol|BOB": "Bs",
	"en-AU|narrowSymbol|BYN": "BYN",
	"en-AU|narrowSymbol|XOF": "XOF",
	"en-AU|symbol|XOF":       "XOF",
	"en-CA|narrowSymbol|BYN": "BYN",
	"en-CA|narrowSymbol|XCG": "Cg",
	"en-CA|symbol|PHP":       "₱",
	"en-CA|symbol|XCG":       "Cg",
	"en-GB|narrowSymbol|BYN": "BYN",
	"en-GB|symbol|PHP":       "₱",
	"en-IN|narrowSymbol|BYN": "BYN",
	"en-IN|symbol|PHP":       "₱",
	"en-IN|symbol|USD":       "$",
	"en-PH|narrowSymbol|BYN": "BYN",
	"en-PH|symbol|JPY":       "¥",
	"en-PH|symbol|USD":       "$",
	"en-SG|narrowSymbol|BYN": "BYN",
	"en-SG|symbol|PHP":       "₱",
	"en-US|narrowSymbol|BYN": "BYN",
	"en-US|symbol|PHP":       "₱",
	"en|narrowSymbol|BYN":    "BYN",
	"en|symbol|PHP":          "₱",
	"es-AR|narrowSymbol|XOF": "XOF",
	"es-AR|symbol|XOF":       "XOF",
	"es-ES|narrowSymbol|XOF": "XOF",
	"es-ES|symbol|CAD":       "CAD",
	"es-ES|symbol|XOF":       "XOF",
	"es-MX|narrowSymbol|MRU": "UM",
	"es-MX|narrowSymbol|XOF": "XOF",
	"es-MX|symbol|MRU":       "UM",
	"es-MX|symbol|XOF":       "XOF",
	"es|narrowSymbol|XOF":    "XOF",
	"es|symbol|CAD":          "CAD",
	"es|symbol|XOF":          "XOF",
	"fi|narrowSymbol|STN":    "STD",
	"fr-CA|narrowSymbol|TOP": "$T",
	"fr-CA|narrowSymbol|WST": "WST",
	"fr-CA|narrowSymbol|XOF": "XOF",
	"fr-CA|symbol|WST":       "WST",
	"fr-CA|symbol|XOF":       "XOF",
	"fr-CH|narrowSymbol|TOP": "$T",
	"fr-CH|narrowSymbol|WST": "$WS",
	"fr-CH|symbol|WST":       "$WS",
	"fr-FR|narrowSymbol|TOP": "$T",
	"fr-FR|narrowSymbol|WST": "$WS",
	"fr-FR|symbol|WST":       "$WS",
	"fr|narrowSymbol|TOP":    "$T",
	"fr|narrowSymbol|WST":    "$WS",
	"fr|symbol|WST":          "$WS",
	"it-IT|narrowSymbol|XCG": "Cf",
	"it-IT|symbol|INR":       "INR",
	"it-IT|symbol|PHP":       "₱",
	"it-IT|symbol|VND":       "VND",
	"it-IT|symbol|XCG":       "Cf",
	"it|narrowSymbol|XCG":    "Cf",
	"it|symbol|INR":          "INR",
	"it|symbol|PHP":          "₱",
	"it|symbol|VND":          "VND",
	"it|symbol|XCG":          "Cf",
	"ja-JP|narrowSymbol|XCG": "Cg",
	"ja-JP|symbol|XCG":       "Cg",
	"ja|narrowSymbol|XCG":    "Cg",
	"ja|symbol|XCG":          "Cg",
	"nl-BE|narrowSymbol|XCG": "Cg",
	"nl-BE|narrowSymbol|ZWG": "ZiG",
	"nl-BE|symbol|ZWG":       "ZiG",
	"nl-NL|narrowSymbol|XCG": "Cg",
	"nl-NL|narrowSymbol|ZWG": "ZiG",
	"nl-NL|symbol|ZWG":       "ZiG",
	"nl|narrowSymbol|XCG":    "Cg",
	"nl|narrowSymbol|ZWG":    "ZiG",
	"nl|symbol|ZWG":          "ZiG",
	"pl|narrowSymbol|BYN":    "BYN",
	"pl|narrowSymbol|XCG":    "XCG",
	"pl|symbol|XCG":          "XCG",
	"pt-BR|narrowSymbol|SYP": "S£",
	"pt|narrowSymbol|SYP":    "S£",
	"ru|narrowSymbol|XCG":    "Cg",
	"ru|symbol|XCG":          "Cg",
	"sv|narrowSymbol|XCG":    "XCG",
	"sv|symbol|BSD":          "BS$",
	"sv|symbol|XCG":          "XCG",
	"th|symbol|THB":          "฿",
	"uk|narrowSymbol|TWD":    "$",
	"vi|symbol|JPY":          "¥",
	"zh-CN|symbol|CNY":       "¥",
	"zh-CN|symbol|KRW":       "₩",
	"zh|symbol|CNY":          "¥",
	"zh|symbol|KRW":          "₩",
}

// currencyDigitCorrections are default fraction digits ICU now uses that
// differ from x/text's copy.
var currencyDigitCorrections = map[string]int{"AMD": 2, "GYD": 2, "HUF": 0, "MNT": 2, "MUR": 2, "RSD": 2, "TZS": 2, "UZS": 2}

type currencyAffix struct {
	symbol string
	form   currencyForm
	space  string
}

// currencyAffixes finds how a locale writes a currency.
func currencyAffixes(options numberOptions) (currencyAffix, error) {
	base, _, region := options.tag.Raw()
	key := base.String()
	if region.String() != "ZZ" {
		key += "-" + region.String()
	}
	form, ok := currencyForms[key]
	if !ok {
		return currencyAffix{}, rangeError("currency formatting in locale %s is not supported", options.locale)
	}
	symbol := options.currency
	if options.currencyDisplay != "code" {
		symbol = currencySymbol(options.tag, key, options.currencyDisplay, options.currency)
	}
	space := form.space
	if space == "" {
		adjacent, _ := utf8.DecodeLastRuneInString(symbol)
		if !form.before {
			adjacent, _ = utf8.DecodeRuneInString(symbol)
		}
		if !unicode.IsSymbol(adjacent) && !unicode.IsSpace(adjacent) {
			space = " "
		}
	}
	return currencyAffix{symbol: symbol, form: form, space: space}, nil
}

var symbolCacheForCurrencies = struct {
	sync.Mutex
	entries map[string]string
}{entries: map[string]string{}}

// currencySymbol is a currency's symbol or narrow symbol in a locale:
// x/text's, corrected where CLDR has changed since. Lookups are cached, since
// a list of prices asks for the same symbol again and again.
func currencySymbol(tag language.Tag, key, display, code string) string {
	cacheKey := tag.String() + "|" + key + "|" + display + "|" + code
	symbolCacheForCurrencies.Lock()
	cached, ok := symbolCacheForCurrencies.entries[cacheKey]
	symbolCacheForCurrencies.Unlock()
	if ok {
		return cached
	}
	symbol := code
	if unit, err := currency.ParseISO(code); err == nil {
		printer := message.NewPrinter(tag)
		symbol = printer.Sprint(currency.Symbol(unit))
		if display == "narrowSymbol" {
			// A currency with no narrow symbol falls back to its symbol.
			if narrow := printer.Sprint(currency.NarrowSymbol(unit)); narrow != code {
				symbol = narrow
			}
		}
	}
	if corrected, ok := currencySymbolCorrections[key+"|"+display+"|"+code]; ok {
		symbol = corrected
	} else if corrected, ok := currencySymbolDefaults[display+"|"+code]; ok {
		symbol = corrected
	}
	symbolCacheForCurrencies.Lock()
	if len(symbolCacheForCurrencies.entries) >= 4096 {
		symbolCacheForCurrencies.entries = map[string]string{}
	}
	symbolCacheForCurrencies.entries[cacheKey] = symbol
	symbolCacheForCurrencies.Unlock()
	return symbol
}

func nativeFormatNumber(args []any) (any, error) {
	options, err := readNumberOptions(args)
	if err != nil {
		return nil, err
	}
	var value decimal
	switch input := argument(args, 2).(type) {
	case float64:
		value = decimalOfFloat(input)
	case int64:
		value = decimalOfFloat(float64(input))
	case string:
		value = decimalOfString(input)
	default:
		return nil, typeError("the value to format must be a number or a string")
	}
	if !value.nan && !value.inf && (value.exponent > 400 || value.exponent < -400) {
		return nil, rangeError("a number of %d digits is more than one call may format here", value.exponent)
	}
	parts, err := options.numberParts(value)
	if err != nil {
		return nil, err
	}
	if asParts, _ := argument(args, 3).(bool); asParts {
		return parts, nil
	}
	var text strings.Builder
	for index := 1; index < len(parts); index += 2 {
		text.WriteString(parts[index].(string))
	}
	return text.String(), nil
}

// ---- Collation ---------------------------------------------------------------------

// collators caches one collator per locale and option set. A collator keeps
// buffers between calls, so each is used by one caller at a time.
var collators = struct {
	sync.Mutex
	entries map[string]*sharedCollator
}{entries: map[string]*sharedCollator{}}

type sharedCollator struct {
	sync.Mutex
	collator *collate.Collator
}

func nativeCompare(args []any) (any, error) {
	left, err := argString(args, 0, "string")
	if err != nil {
		return nil, err
	}
	right, err := argString(args, 1, "that")
	if err != nil {
		return nil, err
	}
	requested, err := canonicalLocales(argument(args, 2))
	if err != nil {
		return nil, err
	}
	raw := argMap(args, 3)
	tag := language.Und
	if len(requested) > 0 {
		if tag, err = parseLocale(requested[0]); err != nil {
			return nil, err
		}
	}
	var options []collate.Option
	key := tag.String()
	if usage := optionString(raw, "usage", "sort"); usage != "sort" {
		return nil, rangeError("collation usage %s is not supported", usage)
	}
	if numeric, _ := raw["numeric"].(bool); numeric || unicodeExtension(tag, "kn") == "true" || unicodeExtension(tag, "kn") == "yes" {
		options = append(options, collate.Numeric)
		key += "|numeric"
	}
	sensitivity := optionString(raw, "sensitivity", "variant")
	switch sensitivity {
	case "base", "case":
		options = append(options, collate.IgnoreCase, collate.IgnoreDiacritics)
	case "accent":
		options = append(options, collate.IgnoreCase)
	case "variant":
	default:
		return nil, rangeError("Value %s out of range for Intl.Collator options property sensitivity", sensitivity)
	}
	if punctuation, _ := raw["ignorePunctuation"].(bool); punctuation {
		return nil, rangeError("ignorePunctuation is not supported")
	}
	if caseFirst := optionString(raw, "caseFirst", "false"); caseFirst != "false" {
		return nil, rangeError("caseFirst %s is not supported", caseFirst)
	}
	if kind := unicodeExtension(tag, "co"); kind != "" && kind != "standard" {
		return nil, rangeError("the collation %s is not supported", kind)
	}

	order := compareWith(tag, key+"|"+map[string]string{"base": "base", "case": "base", "accent": "accent", "variant": "variant"}[sensitivity], options, left, right)
	if sensitivity == "case" && order == 0 {
		// ICU's "case" is the base letters, then their case. x/text has no
		// case level, so strings equal at base are compared again with their
		// accents removed, where what differs is case.
		order = compareWith(tag, key+"|case", nil, withoutMarks(left), withoutMarks(right))
	}
	return int64(order), nil
}

// compareWith compares under a cached collator for the locale and options
// key names.
func compareWith(tag language.Tag, key string, options []collate.Option, left, right string) int {
	collators.Lock()
	shared := collators.entries[key]
	if shared == nil {
		if len(collators.entries) >= 256 {
			collators.entries = map[string]*sharedCollator{}
		}
		shared = &sharedCollator{collator: collate.New(tag, options...)}
		collators.entries[key] = shared
	}
	collators.Unlock()
	shared.Lock()
	defer shared.Unlock()
	return shared.collator.CompareString(left, right)
}

// withoutMarks removes combining marks, after decomposing.
func withoutMarks(text string) string {
	var out strings.Builder
	for _, char := range norm.NFD.String(text) {
		if !unicode.Is(unicode.Mn, char) {
			out.WriteRune(char)
		}
	}
	return out.String()
}

// ---- Week data ---------------------------------------------------------------------

// nativeWeekInfo answers Intl.Locale's week information for a locale: the
// first day of the week and the weekend, from CLDR's supplemental week data
// by region, the region inferred from the language when the locale names
// none. Node no longer reports the minimal days in the first week, and
// neither does this; Luxon then uses its own default of four.
func nativeWeekInfo(args []any) (any, error) {
	name, err := argString(args, 0, "locale")
	if err != nil {
		return nil, err
	}
	tag, canonical, err := parseLocaleName(name)
	if err != nil {
		return nil, err
	}
	region, _ := tag.Region()
	code := region.String()
	for _, subtag := range strings.Split(canonical, "-")[1:] {
		if len(subtag) == 1 {
			break
		}
		if len(subtag) == 2 || len(subtag) == 3 && subtag[0] >= '0' && subtag[0] <= '9' {
			code = subtag // a region the locale names, known or not
			break
		}
	}
	first := 1
	for _, list := range []struct {
		day     int
		regions string
	}{
		{7, weekStartsSunday}, {6, weekStartsSaturday}, {5, "MV"},
	} {
		if strings.Contains(" "+list.regions+" ", " "+code+" ") {
			first = list.day
		}
	}
	weekend := []any{int64(6), int64(7)}
	for _, entry := range weekends {
		if strings.Contains(" "+entry.regions+" ", " "+code+" ") {
			weekend = entry.days
		}
	}
	return map[string]any{"firstDay": int64(first), "weekend": weekend}, nil
}

// CLDR's supplemental week data, by region, held to Node by
// testdata/parity/weeks.json.
const (
	weekStartsSunday   = "AG AS BD BR BS BT BW BZ CA CO DM DO ET GT GU HK HN ID IL IN IS JM JP KE KH KR LA MH MM MO MT MX MZ NI NP NT PA PE PH PK PR PT PY SA SG SV TH TT TW UM US VE VI WS YE ZA ZW"
	weekStartsSaturday = "AF BH DJ DZ EG IQ IR JO KW LY OM QA SD SY"
)

var weekends = []struct {
	regions string
	days    []any
}{
	{"BH DZ EG IL IQ JO KW LY NT OM QA SA SD SY YE", []any{int64(5), int64(6)}},
	{"AF", []any{int64(4), int64(5)}},
	{"IR", []any{int64(5)}},
	{"IN UG", []any{int64(7)}},
}
