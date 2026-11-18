package tools

import (
	"fmt"
	"slices"
	"strings"
)

// language is one language code Doctolib uses, with its ISO 639 code, the
// other ISO 639 codes of the same language (space-separated), its English
// name and its French label.
type language struct {
	code    string
	iso     string
	aliases string
	name    string
	label   string
}

// languages are the language codes Doctolib applies as search filters, found
// on 2026-11-18 by searching with each code. Doctolib's codes are partly
// country codes (gb for English, dz for Kabyle) and partly language codes.
// The labels are Doctolib's own: those of the first 17 from its filter panel,
// the others from practitioners' public pages that list each code.
func languages() []language {
	return []language{
		{"it", "it", "ita", "Italian", "Italien"},
		{"gb", "en", "eng", "English", "Anglais"},
		{"ar", "ar", "ara", "Arabic", "Arabe"},
		{"cn", "zh", "chi zho", "Chinese", "Chinois"},
		{"es", "es", "spa", "Spanish", "Espagnol"},
		{"fr", "fr", "fra fre", "French", "Français"},
		{"gr", "el", "ell gre", "Greek", "Grec"},
		{"de", "de", "deu ger", "German", "Allemand"},
		{"jp", "ja", "jpn", "Japanese", "Japonais"},
		{"sgn", "sgn", "", "Sign language", "Langue des signes"},
		{"fa", "fa", "fas per", "Persian", "Persan"},
		{"pl", "pl", "pol", "Polish", "Polonais"},
		{"pt", "pt", "por", "Portuguese", "Portugais"},
		{"ro", "ro", "ron rum", "Romanian", "Roumain"},
		{"ru", "ru", "rus", "Russian", "Russe"},
		{"tr", "tr", "tur", "Turkish", "Turc"},
		{"ua", "uk", "ukr", "Ukrainian", "Ukrainien"},

		{"dz", "kab", "", "Kabyle", "Kabyle"},
		{"ma", "ber", "", "Berber", "Berbère"},
		{"ir", "ira", "", "Iranian", "Iranien"},
		{"si", "si", "sin", "Sinhala", "Singhalais"},
		{"km", "km", "khm", "Khmer", "Khmer"},
		{"la", "lo", "lao", "Lao", "Laotien"},
		{"lb", "lb", "ltz", "Luxembourgish", "Luxembourgeois"},
		{"ca", "ca", "cat", "Catalan", "Catalan"},
		{"co", "co", "cos", "Corsican", "Corse"},
		{"be", "be", "bel", "Belarusian", "Biélorusse"},
		{"sg", "sg", "sag", "Sango", "Sango"},
		{"af", "af", "afr", "Afrikaans", "Afrikaans"},
		{"kg", "kg", "kon", "Kongo", "Kikongo"},
		{"gu", "gu", "guj", "Gujarati", "Gujarati"},
		{"bm", "bm", "bam", "Bambara", "Bambara"},
		{"ln", "ln", "lin", "Lingala", "Lingala"},

		{"vn", "vi", "vie", "Vietnamese", "Vietnamien"},
		{"il", "he", "heb", "Hebrew", "Hébreu"},
		{"nl", "nl", "dut nld", "Dutch", "Néerlandais"},
		{"se", "sv", "swe", "Swedish", "Suédois"},
		{"no", "no", "nb nn nno nob nor", "Norwegian", "Norvégien"},
		{"cz", "cs", "ces cze", "Czech", "Tchèque"},
		{"hu", "hu", "hun", "Hungarian", "Hongrois"},
		{"bg", "bg", "bul", "Bulgarian", "Bulgare"},
		{"hr", "hr", "hrv", "Croatian", "Croate"},
		{"rs", "sr", "srp", "Serbian", "Serbe"},
		{"sq", "sq", "alb sqi", "Albanian", "Albanais"},
		{"hy", "hy", "arm hye", "Armenian", "Arménien"},
		{"kr", "ko", "kor", "Korean", "Coréen"},
		{"th", "th", "tha", "Thai", "Thaï"},
		{"hi", "hi", "hin", "Hindi", "Hindi"},
		{"ur", "ur", "urd", "Urdu", "Urdu"},
		{"bn", "bn", "ben", "Bengali", "Bengali"},
		{"ta", "ta", "tam", "Tamil", "Tamoul"},
		{"mg", "mg", "mlg", "Malagasy", "Malgache"},
		{"ht", "ht", "hat", "Haitian Creole", "Créole haïtien"},
		{"ku", "ku", "kur", "Kurdish", "Kurde"},
		{"bs", "bs", "bos", "Bosnian", "Bosnien"},
		{"ff", "ff", "ful", "Fula", "Peul"},
		{"gcf", "gcf", "", "Antillean Creole", "Créole Antillais"},
		{"mfe", "mfe", "", "Mauritian Creole", "Créole mauricien"},
		{"rcf", "rcf", "", "Réunion Creole", "Créole réunionnais"},
		{"sk", "sk", "slk slo", "Slovak", "Slovaque"},
		{"so", "so", "som", "Somali", "Somali"},
		{"wol", "wo", "wol", "Wolof", "Wolof"},
		{"aze", "az", "aze", "Azerbaijani", "Azéri"},
		{"dk", "da", "dan", "Danish", "Danois"},
		{"eu", "eu", "baq eus", "Basque", "Basque"},
		{"fi", "fi", "fin", "Finnish", "Finnois"},
		{"id", "id", "ind", "Indonesian", "Indonésien"},
		{"ka", "ka", "geo kat", "Georgian", "Géorgien"},
		{"lt", "lt", "lit", "Lithuanian", "Lituanien"},
		{"rn", "rn", "run", "Kirundi", "Kirundi"},
		{"snk", "snk", "", "Soninke", "Soniké"},
	}
}

// sectors are the regulationSector codes Doctolib filters on, with the labels
// of its filter panel, in its order: one set for doctors, one for professions
// such as dentists, and two for organisations.
func sectors() [][2]string {
	return [][2]string{
		{"contracted_1", "Secteur 1"},
		{"contracted_1_with_extra", "Secteur 1 avec droit permanent à dépassement"},
		{"contracted_1_with_optam", "Secteur 1 OPTAM"},
		{"contracted_1_with_optam_co", "Secteur 1 OPTAM-CO"},
		{"contracted_2", "Secteur 2"},
		{"contracted_2_with_optam", "Secteur 2 OPTAM"},
		{"contracted_2_with_optam_co", "Secteur 2 OPTAM-CO"},
		{"non_contracted", "Secteur 3 (non conventionné)"},
		{"contracted", "Conventionné"},
		{"contracted_with_extra", "Conventionné avec dépassements d’honoraires"},
		{"organization_contracted", "Etablissement conventionné"},
		{"organization_non_contracted", "Etablissement non conventionné"},
	}
}

func sectorLabels() map[string]string {
	labels := map[string]string{}
	for _, s := range sectors() {
		labels[s[0]] = s[1]
	}
	return labels
}

func sectorCodes() []string {
	codes := make([]string, 0, len(sectors()))
	for _, s := range sectors() {
		codes = append(codes, s[0])
	}
	return codes
}

func label(table map[string]string, code string) Label {
	l, ok := table[code]
	if !ok {
		return Label{Code: code, Label: nil}
	}
	return Label{Code: code, Label: &l}
}

// Language is a language of a result: Doctolib's code with its ISO 639 code, English name and French label.
type Language struct {
	Code  string  `json:"code"`
	ISO   *string `json:"iso"`
	Name  *string `json:"name"`
	Label *string `json:"label"`
}

// languageOf describes Doctolib's code; a code the table does not know has a null ISO code, name and label.
func languageOf(code string) Language {
	for _, l := range languages() {
		if l.code == code {
			return Language{Code: code, ISO: &l.iso, Name: &l.name, Label: &l.label}
		}
	}
	return Language{Code: code, ISO: nil, Name: nil, Label: nil}
}

// doctolibLanguages turns ISO 639 codes, of any of its parts, into Doctolib's.
// A code the table does not know is sent as it is and returned in unknown. A
// code that is Doctolib's own spelling of another language is refused, since
// sending it would search for that other language.
func doctolibLanguages(isos []string) ([]string, []string, error) {
	var codes, unknown []string
	for _, iso := range isos {
		if i := slices.IndexFunc(languages(), func(l language) bool {
			return l.iso == iso || slices.Contains(strings.Fields(l.aliases), iso)
		}); i >= 0 {
			codes = append(codes, languages()[i].code)
			continue
		}
		if i := slices.IndexFunc(languages(), func(l language) bool { return l.code == iso }); i >= 0 {
			l := languages()[i]
			return nil, nil, fmt.Errorf("languages: the server knows no language with the ISO 639 code %q; "+
				"Doctolib uses %q for %s (ISO %q), so it is not sent as it is", iso, iso, l.name, l.iso)
		}
		codes = append(codes, iso)
		unknown = append(unknown, iso)
	}
	return codes, unknown, nil
}

// languageList is the ISO codes and names for the tool description.
func languageList() string {
	parts := make([]string, 0, len(languages()))
	for _, l := range languages() {
		parts = append(parts, l.iso+" "+l.name)
	}
	return strings.Join(parts, ", ")
}
