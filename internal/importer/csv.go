package importer

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Record is the source-neutral representation copied into the database.
type Record struct {
	SourceID         string
	IINStart         string
	IINEnd           string
	IINLength        int16
	Start8           int64
	End8             int64
	NumberLength     *int16
	Luhn             *bool
	Scheme           string
	Brand            string
	CardType         string
	CardLevel        string
	Prepaid          *bool
	CountryAlpha2    string
	CountryAlpha3    string
	CountryName      string
	CountryCurrency  string
	CountryLatitude  *float64
	CountryLongitude *float64
	BankName         string
	BankURL          string
	BankPhone        string
	BankCity         string
	BankLogo         string
	UpdatedAt        time.Time
}

type columnMap struct {
	start, end, numberLength, luhn                   int
	scheme, brand, cardType, cardLevel, prepaid      int
	alpha2, alpha3, countryName, currency            int
	latitude, longitude                              int
	bankName, bankURL, bankPhone, bankCity, bankLogo int
}

// CSVSource streams parsed records without coupling this package to any
// specific database driver.
type CSVSource struct {
	reader   *csv.Reader
	columns  columnMap
	sourceID string
	updated  time.Time
	current  Record
	err      error
	total    int64
	invalid  int64
}

func NewCSVSource(reader io.Reader, sourceID, format string, updated time.Time) (*CSVSource, error) {
	csvReader := csv.NewReader(reader)
	csvReader.FieldsPerRecord = -1
	csvReader.LazyQuotes = true
	csvReader.TrimLeadingSpace = true
	header, err := csvReader.Read()
	if err != nil {
		return nil, fmt.Errorf("read CSV header: %w", err)
	}
	columns, err := mapColumns(header, format)
	if err != nil {
		return nil, err
	}
	return &CSVSource{reader: csvReader, columns: columns, sourceID: sourceID, updated: updated}, nil
}

func (s *CSVSource) Next() bool {
	if s.err != nil {
		return false
	}
	for {
		row, err := s.reader.Read()
		if errors.Is(err, io.EOF) {
			return false
		}
		if err != nil {
			s.err = fmt.Errorf("read CSV row %d: %w", s.total+2, err)
			return false
		}
		s.total++
		record, err := s.parse(row)
		if err != nil {
			s.invalid++
			continue
		}
		s.current = record
		return true
	}
}

func (s *CSVSource) Values() ([]any, error) {
	r := s.current
	return []any{
		r.SourceID, r.IINStart, r.IINEnd, r.IINLength, r.Start8, r.End8,
		r.NumberLength, r.Luhn, r.Scheme, r.Brand, r.CardType, r.CardLevel, r.Prepaid,
		r.CountryAlpha2, r.CountryAlpha3, r.CountryName, r.CountryCurrency,
		r.CountryLatitude, r.CountryLongitude,
		r.BankName, r.BankURL, r.BankPhone, r.BankCity, r.BankLogo, r.UpdatedAt,
	}, nil
}

func (s *CSVSource) Err() error         { return s.err }
func (s *CSVSource) TotalRows() int64   { return s.total }
func (s *CSVSource) InvalidRows() int64 { return s.invalid }

func (s *CSVSource) parse(row []string) (Record, error) {
	start := digits(field(row, s.columns.start))
	end := digits(field(row, s.columns.end))
	if start == "" || len(start) > 8 {
		return Record{}, errors.New("invalid IIN start")
	}
	if end == "" {
		end = start
	}
	if len(end) != len(start) || len(end) > 8 || end < start {
		return Record{}, errors.New("invalid IIN range")
	}
	start8, end8, err := NormalizeRange(start, end)
	if err != nil {
		return Record{}, err
	}

	record := Record{
		SourceID: s.sourceID, IINStart: start, IINEnd: end, IINLength: int16(len(start)), Start8: start8, End8: end8,
		Scheme: enum(field(row, s.columns.scheme)), Brand: clean(field(row, s.columns.brand)),
		CardType: enum(field(row, s.columns.cardType)), CardLevel: enum(field(row, s.columns.cardLevel)),
		CountryAlpha2: strings.ToUpper(clean(field(row, s.columns.alpha2))),
		CountryAlpha3: strings.ToUpper(clean(field(row, s.columns.alpha3))),
		CountryName:   clean(field(row, s.columns.countryName)), CountryCurrency: strings.ToUpper(clean(field(row, s.columns.currency))),
		BankName: clean(field(row, s.columns.bankName)), BankURL: clean(field(row, s.columns.bankURL)),
		BankPhone: clean(field(row, s.columns.bankPhone)), BankCity: clean(field(row, s.columns.bankCity)),
		BankLogo: clean(field(row, s.columns.bankLogo)), UpdatedAt: s.updated,
	}
	record.NumberLength = parseInt16(field(row, s.columns.numberLength))
	record.Luhn = parseBool(field(row, s.columns.luhn))
	record.Prepaid = parseBool(field(row, s.columns.prepaid))
	record.CountryLatitude = parseFloat(field(row, s.columns.latitude), -90, 90)
	record.CountryLongitude = parseFloat(field(row, s.columns.longitude), -180, 180)
	if len(record.CountryAlpha2) != 2 {
		record.CountryAlpha2 = ""
	}
	if len(record.CountryAlpha3) != 3 {
		record.CountryAlpha3 = ""
	}
	return record, nil
}

func mapColumns(header []string, format string) (columnMap, error) {
	normalized := make([]string, len(header))
	for i, name := range header {
		normalized[i] = normalizeHeader(name)
	}
	find := func(names ...string) int {
		for _, wanted := range names {
			for i, actual := range normalized {
				if actual == wanted {
					return i
				}
			}
		}
		return -1
	}

	start := find("iin_start", "iinstart", "range_start", "bin_start", "binstart", "bin", "iin", "prefix")
	// A maintained community file has historically replaced the first "BIN"
	// header cell with digits while leaving every other header intact. Detect
	// that shape so one upstream typo does not take the whole service offline.
	if start < 0 && len(normalized) > 1 && digits(normalized[0]) != "" && find("type", "category", "issuer") >= 0 {
		start = 0
	}
	if start < 0 {
		return columnMap{}, errors.New("CSV has no BIN/IIN start column")
	}

	columns := columnMap{
		start: start, end: find("iin_end", "iinend", "range_end", "bin_end", "binend"),
		numberLength: find("number_length", "card_number_length", "length"), luhn: find("luhn"),
		cardType: find("type", "card_type", "funding"), cardLevel: find("category", "level", "card_level"),
		prepaid:     find("prepaid", "is_prepaid"),
		alpha2:      find("alpha_2", "alpha2", "iso_code_2", "iso_code2", "isocode2", "country_code", "country"),
		alpha3:      find("alpha_3", "alpha3", "iso_code_3", "iso_code3", "isocode3"),
		countryName: find("country_name"), currency: find("currency", "country_currency"),
		latitude: find("latitude", "country_latitude"), longitude: find("longitude", "country_longitude"),
		bankName:  find("issuer", "bank_name", "bank", "issuer_name"),
		bankURL:   find("issuer_url", "bank_url", "bank_website", "website"),
		bankPhone: find("issuer_phone", "bank_phone", "phone"),
		bankCity:  find("bank_city", "city"), bankLogo: find("bank_logo", "logo"),
	}
	switch format {
	case "ranges":
		columns.scheme = find("scheme", "network")
		columns.brand = find("brand", "card_brand", "product")
	case "binlist":
		columns.scheme = find("scheme", "network", "brand")
		columns.brand = find("card_brand", "product")
	default:
		columns.scheme = find("scheme", "network", "brand")
		columns.brand = find("card_brand", "product")
	}
	return columns, nil
}

func NormalizeRange(start, end string) (int64, int64, error) {
	if len(start) < 1 || len(start) > 8 || len(end) != len(start) {
		return 0, 0, errors.New("IIN range must use 1 to 8 digits of equal width")
	}
	startValue, err := strconv.ParseInt(start, 10, 64)
	if err != nil {
		return 0, 0, errors.New("invalid IIN start")
	}
	endValue, err := strconv.ParseInt(end, 10, 64)
	if err != nil || endValue < startValue {
		return 0, 0, errors.New("invalid IIN end")
	}
	scale := int64(1)
	for i := len(start); i < 8; i++ {
		scale *= 10
	}
	return startValue * scale, (endValue+1)*scale - 1, nil
}

func normalizeHeader(value string) string {
	value = strings.TrimSpace(strings.TrimPrefix(value, "\ufeff"))
	var normalized strings.Builder
	var previous rune
	for index, current := range value {
		if index > 0 && unicode.IsUpper(current) && (unicode.IsLower(previous) || unicode.IsDigit(previous)) {
			normalized.WriteByte('_')
		}
		normalized.WriteRune(unicode.ToLower(current))
		previous = current
	}
	value = strings.ReplaceAll(normalized.String(), `\_`, "_")
	replacer := strings.NewReplacer("-", "_", " ", "_", "/", "_", ".", "")
	return replacer.Replace(value)
}

func field(row []string, index int) string {
	if index < 0 || index >= len(row) {
		return ""
	}
	return row[index]
}

func digits(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return value
}

func clean(value string) string {
	value = strings.ToValidUTF8(strings.TrimSpace(value), "")
	if len(value) > 2_000 {
		value = strings.ToValidUTF8(value[:2_000], "")
	}
	return value
}

func enum(value string) string { return strings.ToLower(clean(value)) }

func parseBool(value string) *bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y":
		result := true
		return &result
	case "0", "false", "no", "n":
		result := false
		return &result
	default:
		return nil
	}
}

func parseInt16(value string) *int16 {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 16)
	if err != nil || parsed < 1 {
		return nil
	}
	result := int16(parsed)
	return &result
}

func parseFloat(value string, minimum, maximum float64) *float64 {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || parsed < minimum || parsed > maximum {
		return nil
	}
	return &parsed
}
