package tools

import (
	"fmt"
	"slices"
	"strings"
)

// language is one language code Doctolib uses, with its ISO 639 code, its English name and its French label.
type language struct {
	code  string
	iso   string
	name  string
	label string
}

// languages are the language codes Doctolib applies as search filters, found
// on 2026-11-18 by searching with each code. Doctolib's codes are partly
// country codes (gb for English, dz for Kabyle) and partly language codes.
// The labels are Doctolib's own: those of the first 17 from its filter panel,
// the others from practitioners' public pages that list each code.
func languages() []language {
	return []language{
		{"it", "it", "Italian", "Italien"},
		{"gb", "en", "English", "Anglais"},
		{"ar", "ar", "Arabic", "Arabe"},
		{"cn", "zh", "Chinese", "Chinois"},
		{"es", "es", "Spanish", "Espagnol"},
		{"fr", "fr", "French", "Français"},
		{"gr", "el", "Greek", "Grec"},
		{"de", "de", "German", "Allemand"},
		{"jp", "ja", "Japanese", "Japonais"},
		{"sgn", "sgn", "Sign language", "Langue des signes"},
		{"fa", "fa", "Persian", "Persan"},
		{"pl", "pl", "Polish", "Polonais"},
		{"pt", "pt", "Portuguese", "Portugais"},
		{"ro", "ro", "Romanian", "Roumain"},
		{"ru", "ru", "Russian", "Russe"},
		{"tr", "tr", "Turkish", "Turc"},
		{"ua", "uk", "Ukrainian", "Ukrainien"},

		{"dz", "kab", "Kabyle", "Kabyle"},
		{"ma", "ber", "Berber", "Berbère"},
		{"ir", "ira", "Iranian", "Iranien"},
		{"si", "si", "Sinhala", "Singhalais"},
		{"km", "km", "Khmer", "Khmer"},
		{"la", "lo", "Lao", "Laotien"},
		{"lb", "lb", "Luxembourgish", "Luxembourgeois"},
		{"ca", "ca", "Catalan", "Catalan"},
		{"co", "co", "Corsican", "Corse"},
		{"be", "be", "Belarusian", "Biélorusse"},
		{"sg", "sg", "Sango", "Sango"},
		{"af", "af", "Afrikaans", "Afrikaans"},
		{"kg", "kg", "Kongo", "Kikongo"},
		{"gu", "gu", "Gujarati", "Gujarati"},
		{"bm", "bm", "Bambara", "Bambara"},
		{"ln", "ln", "Lingala", "Lingala"},

		{
			"vn",
			"vi",
			"Vietnamese",
			"Vietnamien",
		},
		{"il", "he", "Hebrew", "Hébreu"},
		{"nl", "nl", "Dutch", "Néerlandais"},
		{"se", "sv", "Swedish", "Suédois"},
		{"no", "no", "Norwegian", "Norvégien"},
		{"cz", "cs", "Czech", "Tchèque"},
		{"hu", "hu", "Hungarian", "Hongrois"},
		{"bg", "bg", "Bulgarian", "Bulgare"},
		{"hr", "hr", "Croatian", "Croate"},
		{"rs", "sr", "Serbian", "Serbe"},
		{"sq", "sq", "Albanian", "Albanais"},
		{"hy", "hy", "Armenian", "Arménien"},
		{"kr", "ko", "Korean", "Coréen"},
		{"th", "th", "Thai", "Thaï"},
		{"hi", "hi", "Hindi", "Hindi"},
		{"ur", "ur", "Urdu", "Urdu"},
		{"bn", "bn", "Bengali", "Bengali"},
		{"ta", "ta", "Tamil", "Tamoul"},
		{"mg", "mg", "Malagasy", "Malgache"},
		{"ht", "ht", "Haitian Creole", "Créole haïtien"},
		{"ku", "ku", "Kurdish", "Kurde"},
		{"bs", "bs", "Bosnian", "Bosnien"},
		{"ff", "ff", "Fula", "Peul"},
		{"gcf", "gcf", "Antillean Creole", "Créole Antillais"},
		{"mfe", "mfe", "Mauritian Creole", "Créole mauricien"},
		{"rcf", "rcf", "Réunion Creole", "Créole réunionnais"},
		{"sk", "sk", "Slovak", "Slovaque"},
		{"so", "so", "Somali", "Somali"},
		{"wol", "wo", "Wolof", "Wolof"},
		{"aze", "az", "Azerbaijani", "Azéri"},
		{"dk", "da", "Danish", "Danois"},
		{"eu", "eu", "Basque", "Basque"},
		{"fi", "fi", "Finnish", "Finnois"},
		{"id", "id", "Indonesian", "Indonésien"},
		{"ka", "ka", "Georgian", "Géorgien"},
		{"lt", "lt", "Lithuanian", "Lituanien"},
		{"rn", "rn", "Kirundi", "Kirundi"},
		{"snk", "snk", "Soninke", "Soniké"},
	}
}

// sectorLabels maps the regulationSector codes Doctolib filters on to the
// labels of its filter panel: one set for doctors, one for professions such as
// dentists, and two for organisations.
func sectorLabels() map[string]string {
	return map[string]string{
		"contracted_1":                "Secteur 1",
		"contracted_1_with_extra":     "Secteur 1 avec droit permanent à dépassement",
		"contracted_1_with_optam":     "Secteur 1 OPTAM",
		"contracted_1_with_optam_co":  "Secteur 1 OPTAM-CO",
		"contracted_2":                "Secteur 2",
		"contracted_2_with_optam":     "Secteur 2 OPTAM",
		"contracted_2_with_optam_co":  "Secteur 2 OPTAM-CO",
		"non_contracted":              "Secteur 3 (non conventionné)",
		"contracted":                  "Conventionné",
		"contracted_with_extra":       "Conventionné avec dépassements d’honoraires",
		"organization_contracted":     "Etablissement conventionné",
		"organization_non_contracted": "Etablissement non conventionné",
	}
}

// sectorCodes are the regulationSector codes Doctolib filters on, in the order of its filter panels.
func sectorCodes() []string {
	return []string{
		"contracted_1", "contracted_1_with_extra", "contracted_1_with_optam", "contracted_1_with_optam_co",
		"contracted_2", "contracted_2_with_optam", "contracted_2_with_optam_co", "non_contracted",
		"contracted", "contracted_with_extra",
		"organization_contracted", "organization_non_contracted",
	}
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

// doctolibLanguages turns ISO 639 codes into Doctolib's. A code the table does
// not know is sent as it is and returned in unknown. A code that is Doctolib's
// own spelling of another language is refused, since sending it would search
// for that other language.
func doctolibLanguages(isos []string) ([]string, []string, error) {
	var codes, unknown []string
	for _, iso := range isos {
		if i := slices.IndexFunc(languages(), func(l language) bool { return l.iso == iso }); i >= 0 {
			codes = append(codes, languages()[i].code)
			continue
		}
		if i := slices.IndexFunc(languages(), func(l language) bool { return l.code == iso }); i >= 0 {
			l := languages()[i]
			return nil, nil, fmt.Errorf("languages: %q is Doctolib's own code for %s; pass the ISO 639 code %q",
				iso, l.name, l.iso)
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
