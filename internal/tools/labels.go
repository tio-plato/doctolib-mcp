package tools

// languageLabels maps Doctolib's language codes, which are not ISO codes, to
// the labels its /patient-health-search/api/v1/filters endpoint gives them.
func languageLabels() map[string]string {
	return map[string]string{}
}

// sectorLabels maps the regulationSector codes of search results to Doctolib's labels.
func sectorLabels() map[string]string {
	return map[string]string{}
}

func label(table map[string]string, code string) Label {
	l, ok := table[code]
	if !ok {
		return Label{Code: code, Label: nil}
	}
	return Label{Code: code, Label: &l}
}
