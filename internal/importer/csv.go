package importer

import (
	"bufio"
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

// FormatOpenBIIN is the sharded OpenBIIN layout: one BIN6 prefix per row plus a
// pipe-separated list of sub-ranges covering the remaining PAN digits.
const FormatOpenBIIN = "openbiin"

// openbiinPrefixLength is the fixed 6-digit BIN6 column width required by the
// upstream contribution rules; openbiinRangeDigits is the sub-range width, which
// covers the 7th and 8th PAN digits so BIN6 + block fills exactly 8 digits.
const (
	openbiinPrefixLength = 6
	openbiinRangeDigits  = 2
)

// CSVSource streams parsed records without coupling this package to any
// specific database driver.
//
// Most formats map one CSV row to exactly one record. OpenBIIN rows describe
// several 8-digit sub-ranges at once (for example "457100,40-45|51-53"), so
// Next() may yield more than one record for a single row. The extra records
// live in pending, which holds at most the sub-ranges of the row currently
// being read, so memory stays bounded no matter how many blocks a row lists.
type CSVSource struct {
	reader   rowReader
	columns  columnMap
	format   string
	sourceID string
	updated  time.Time
	current  Record
	pending  []Record
	err      error
	total    int64
	invalid  int64
	ranges   int64
}

// rowReader yields the fields of one CSV record at a time. The default
// implementation is encoding/csv; formats whose upstream guarantees one record
// per line use lineReader instead.
type rowReader interface {
	Read() ([]string, error)
}

// lineReader parses exactly one line per record.
//
// encoding/csv treats a field that opens with a quote and then continues after
// the closing quote as an unterminated quoted field, so it keeps consuming
// following lines until the next stray quote. OpenBIIN's issuer names contain
// 126 values of that shape — the live data has rows such as
//
//	540294,00,"ARMENIAN CARD" CJSC,AM,mastercard,credit
//
// and reading the 100 shards with encoding/csv merged 4,520 real rows into
// neighbouring records, silently dropping them. OpenBIIN's own contribution
// rules forbid commas inside a column and require the whole value to be quoted
// when one appears, and its production reader splits the file on newlines, so
// every record is on exactly one line. Reading line by line keeps one malformed
// issuer name from costing every record after it.
type lineReader struct {
	scanner *bufio.Scanner
	line    strings.Reader
	reader  *csv.Reader
}

func newLineReader(reader io.Reader) *lineReader {
	scanner := bufio.NewScanner(reader)
	// Issuer names are short; the ceiling only guards against a runaway line.
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	return &lineReader{scanner: scanner}
}

func (r *lineReader) Read() ([]string, error) {
	for {
		if !r.scanner.Scan() {
			if err := r.scanner.Err(); err != nil {
				return nil, err
			}
			return nil, io.EOF
		}
		line := r.scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		r.line.Reset(line)
		r.reader = csv.NewReader(&r.line)
		r.reader.FieldsPerRecord = -1
		r.reader.LazyQuotes = true
		r.reader.TrimLeadingSpace = true
		return r.reader.Read()
	}
}

func NewCSVSource(source io.Reader, sourceID, format string, updated time.Time) (*CSVSource, error) {
	var reader rowReader
	if format == FormatOpenBIIN {
		reader = newLineReader(source)
	} else {
		csvReader := csv.NewReader(source)
		csvReader.FieldsPerRecord = -1
		csvReader.LazyQuotes = true
		csvReader.TrimLeadingSpace = true
		reader = csvReader
	}
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read CSV header: %w", err)
	}
	columns, err := mapColumns(header, format)
	if err != nil {
		return nil, err
	}
	return &CSVSource{reader: reader, columns: columns, format: format, sourceID: sourceID, updated: updated}, nil
}

func (s *CSVSource) Next() bool {
	if s.err != nil {
		return false
	}
	// Drain the remaining records of the row read previously.
	if len(s.pending) > 0 {
		s.current = s.pending[0]
		s.pending = s.pending[1:]
		return true
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
		records, dropped := s.parseRow(row)
		if len(records) == 0 {
			s.invalid++
			continue
		}
		// A sub-range the upstream file lists but that cannot be represented is
		// not fatal on its own, but it is counted so a source that silently
		// loses data is visible in the import log and in sources.skipped_rows.
		s.ranges += int64(dropped)
		s.current = records[0]
		s.pending = records[1:]
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

// InvalidRanges reports how many sub-range blocks were dropped from rows that
// were otherwise imported. It is always zero for one-record-per-row formats.
func (s *CSVSource) InvalidRanges() int64 { return s.ranges }

// parseRow maps one CSV row to the records it describes. dropped counts blocks
// within the row that could not be represented; a row that yields no records at
// all is skipped by Next and counted as invalid.
func (s *CSVSource) parseRow(row []string) (records []Record, dropped int) {
	if s.format == FormatOpenBIIN {
		return s.parseOpenBIIN(row)
	}
	record, err := s.parse(row)
	if err != nil {
		return nil, 0
	}
	return []Record{record}, 0
}

// parseOpenBIIN expands one OpenBIIN row into its 8-digit sub-range records.
//
// Upstream stores a fixed 6-digit BIN6 plus a "Ranges" column holding the 7th
// and 8th PAN digits as pipe-separated contiguous blocks ("00-19|50-99"). A
// block is either "NN-MM" or a single "NN". Each block becomes one record whose
// IIN range is BIN6+first..BIN6+last, which is exactly the range shape the
// bin_records containment index already answers.
//
// Blocks that are blank, non-numeric, mixed-width or reversed are dropped
// rather than guessed at, and rows that lose every block are skipped.
func (s *CSVSource) parseOpenBIIN(row []string) ([]Record, int) {
	bin6 := digits(field(row, s.columns.start))
	if len(bin6) != openbiinPrefixLength {
		return nil, 0
	}
	blocks := strings.Split(field(row, s.columns.end), "|")
	records := make([]Record, 0, len(blocks))
	dropped := 0
	for _, block := range blocks {
		first, last, ok := openbiinBlock(block)
		if !ok {
			dropped++
			continue
		}
		start, end := bin6+first, bin6+last
		start8, end8, err := NormalizeRange(start, end)
		if err != nil {
			dropped++
			continue
		}
		records = append(records, s.buildRecord(row, start, end, start8, end8))
	}
	return records, dropped
}

// openbiinBlock parses one pipe-separated sub-range block into its first and
// last digit strings.
//
// Both ends must be exactly openbiinRangeDigits digits. Upstream's rules and
// every block in the live data use the two digits after BIN6 ("05" for the
// single 8-digit BIN ending 05, "00-99" for the whole block). A wider block
// would not fit the 8-digit space this schema stores, and a narrower one would
// describe an ambiguous 7-digit prefix rather than a real issuer range, so both
// are rejected instead of guessed at.
func openbiinBlock(block string) (string, string, bool) {
	block = strings.TrimSpace(block)
	if block == "" {
		return "", "", false
	}
	first, last := block, block
	if dash := strings.IndexByte(block, '-'); dash >= 0 {
		first, last = strings.TrimSpace(block[:dash]), strings.TrimSpace(block[dash+1:])
	}
	first, last = digits(first), digits(last)
	if first == "" || last == "" {
		return "", "", false
	}
	if len(first) != openbiinRangeDigits || len(last) != openbiinRangeDigits {
		return "", "", false
	}
	// Equal-width digit strings compare numerically under string ordering.
	if last < first {
		return "", "", false
	}
	return first, last, true
}

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
	return s.buildRecord(row, start, end, start8, end8), nil
}

// buildRecord copies the row's shared metadata onto one IIN range. Every format
// funnels through here so a row that expands into several ranges carries
// identical issuer, country and type values on each of them.
func (s *CSVSource) buildRecord(row []string, start, end string, start8, end8 int64) Record {
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
	return record
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

	startAliases := []string{"iin_start", "iinstart", "range_start", "bin_start", "binstart", "bin", "iin", "prefix"}
	endAliases := []string{"iin_end", "iinend", "range_end", "bin_end", "binend"}
	if format == FormatOpenBIIN {
		// OpenBIIN names its columns BIN6/Ranges rather than IIN start/end.
		startAliases = []string{"bin6", "bin", "iin", "iin_start", "prefix"}
		endAliases = []string{"ranges", "range", "sub_ranges", "subranges", "iin_end"}
	}
	start := find(startAliases...)
	// A maintained community file has historically replaced the first "BIN"
	// header cell with digits while leaving every other header intact. Detect
	// that shape so one upstream typo does not take the whole service offline.
	if start < 0 && format != FormatOpenBIIN && len(normalized) > 1 && digits(normalized[0]) != "" && find("type", "category", "issuer") >= 0 {
		start = 0
	}
	if start < 0 {
		return columnMap{}, errors.New("CSV has no BIN/IIN start column")
	}
	end := find(endAliases...)
	if format == FormatOpenBIIN && end < 0 {
		// Importing an OpenBIIN file without its sub-range column would install
		// one 6-digit record per row and silently discard every narrower range,
		// so this is a hard error rather than a fallback.
		return columnMap{}, errors.New("openbiin CSV has no Ranges column")
	}

	columns := columnMap{
		start:        start,
		end:          end,
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
	case FormatOpenBIIN:
		// Upstream's "Brand" is the payment network (visa, mastercard), the same
		// meaning bin-list-data's Brand column carries, so it maps to scheme and
		// the product-level brand stays empty.
		columns.scheme = find("brand", "scheme", "network")
		columns.brand = find("card_brand", "product")
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
