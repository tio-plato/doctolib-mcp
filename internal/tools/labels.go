package tools

import (
	"maps"
	"slices"
)

// languageNames are the language codes Doctolib's /patient-health-search/api/v1/filters
// endpoint listed on 2026-11-17, with the language each code stands for. Doctolib
// gives no names: they are read from the codes, which are not ISO codes.
func languageNames() map[string]string {
	return map[string]string{
		"ar": "Arabic", "cn": "Chinese", "de": "German", "es": "Spanish", "fa": "Persian", "fr": "French",
		"gb": "English", "gr": "Greek", "it": "Italian", "jp": "Japanese", "pl": "Polish", "pt": "Portuguese",
		"ro": "Romanian", "ru": "Russian", "sgn": "sign language", "tr": "Turkish", "ua": "Ukrainian",
	}
}

// languageLabels maps language codes to Doctolib's own labels. Its /filters
// endpoint gives none, so the table is empty and every label is null.
func languageLabels() map[string]string {
	return map[string]string{}
}

// sectorLabels maps regulationSector codes to Doctolib's own labels; /filters gives none either.
func sectorLabels() map[string]string {
	return map[string]string{}
}

// sectorCodes are the regulationSector codes of search results that Doctolib's
// /filters endpoint offers as filters, in upper case: one set for doctors, one
// for professions such as dentists, and two for organisations.
func sectorCodes() []string {
	return []string{
		"contracted_1", "contracted_1_with_extra", "contracted_1_with_optam", "contracted_1_with_optam_co",
		"contracted_2", "contracted_2_with_optam", "contracted_2_with_optam_co", "non_contracted",
		"contracted", "contracted_with_extra",
		"organization_contracted", "organization_non_contracted",
	}
}

func languageCodes() []string {
	return slices.Sorted(maps.Keys(languageNames()))
}

func label(table map[string]string, code string) Label {
	l, ok := table[code]
	if !ok {
		return Label{Code: code, Label: nil}
	}
	return Label{Code: code, Label: &l}
}
