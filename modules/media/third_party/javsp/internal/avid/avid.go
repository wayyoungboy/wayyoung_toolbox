package avid

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yuukiy/javsp-go/internal/model"
)

var (
	fc2Pattern         = regexp.MustCompile(`(?i)FC2[^A-Z\d]{0,5}(PPV[^A-Z\d]{0,5})?(\d{5,7})`)
	heydougaPattern    = regexp.MustCompile(`(?i)(HEYDOUGA)[-_]*(\d{4})[-_]0?(\d{3,5})`)
	getchuPattern      = regexp.MustCompile(`(?i)GETCHU[-_]*(\d+)`)
	gyuttoPattern      = regexp.MustCompile(`(?i)GYUTTO-(\d+)`)
	luxuPattern        = regexp.MustCompile(`(?i)259LUXU-(\d+)`)
	domainPattern      = regexp.MustCompile(`(?i)\w{3,10}\.(COM|NET|APP|XYZ)`)
	heyPattern         = regexp.MustCompile(`(?i)(?:HEY)[-_]*(\d{4})[-_]0?(\d{3,5})`)
	mugenPattern       = regexp.MustCompile(`(?i)(MKB?D)[-_]*(S\d{2,3})|(MK3D2DBD|S2M|S2MBD)[-_]*(\d{2,3})`)
	ibwPattern         = regexp.MustCompile(`(?i)(IBW)[-_](\d{2,5}z)`)
	normalSepPattern   = regexp.MustCompile(`(?i)([A-Z]{2,10})[-_](\d{2,5})`)
	tokyoHotRESKYEX    = regexp.MustCompile(`(?i)(RED[01]\d\d|SKY[0-3]\d\d|EX00[01]\d)`)
	normalNoSepPattern = regexp.MustCompile(`(?i)([A-Z]{2,})(\d{2,5})`)
	tmaPattern         = regexp.MustCompile(`(T[23]8[-_]\d{3})`)
	tokyoHotNKPattern  = regexp.MustCompile(`(?i)(N\d{4}|K\d{4})`)
	r18Pattern         = regexp.MustCompile(`(?i)R18-?\d{3}`)
	numericPattern     = regexp.MustCompile(`(\d{6}[-_]\d{2,3})`)
	cdPostfixPattern   = regexp.MustCompile(`([-_]\w|cd\d)$`)
)

func GetID(path string, ignoredPatterns []string) string {
	base := filepath.Base(path)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	norm := strings.ToUpper(stem)
	if len(ignoredPatterns) > 0 {
		ignorePattern, err := regexp.Compile(strings.Join(ignoredPatterns, "|"))
		if err == nil {
			norm = ignorePattern.ReplaceAllString(norm, "")
		}
	}

	if strings.Contains(norm, "FC2") {
		if match := fc2Pattern.FindStringSubmatch(norm); len(match) > 2 {
			return "FC2-" + match[2]
		}
	} else if strings.Contains(norm, "HEYDOUGA") {
		if match := heydougaPattern.FindStringSubmatch(norm); len(match) > 3 {
			return strings.Join(match[1:4], "-")
		}
	} else if strings.Contains(norm, "GETCHU") {
		if match := getchuPattern.FindStringSubmatch(norm); len(match) > 1 {
			return "GETCHU-" + match[1]
		}
	} else if strings.Contains(norm, "GYUTTO") {
		if match := gyuttoPattern.FindStringSubmatch(norm); len(match) > 1 {
			return "GYUTTO-" + match[1]
		}
	} else if strings.Contains(norm, "259LUXU") {
		if match := luxuPattern.FindStringSubmatch(norm); len(match) > 1 {
			return "259LUXU-" + match[1]
		}
	} else {
		noDomain := domainPattern.ReplaceAllString(norm, "")
		if noDomain != norm {
			if avid := GetID(noDomain, ignoredPatterns); avid != "" {
				return avid
			}
		}
		if match := heyPattern.FindStringSubmatch(norm); len(match) > 2 {
			return "heydouga-" + strings.Join(match[1:3], "-")
		}
		if match := mugenPattern.FindStringSubmatch(norm); len(match) > 0 {
			if match[1] != "" {
				return match[1] + "-" + match[2]
			}
			return match[3] + "-" + match[4]
		}
		if match := ibwPattern.FindStringSubmatch(norm); len(match) > 2 {
			return match[1] + "-" + match[2]
		}
		if match := normalSepPattern.FindStringSubmatch(norm); len(match) > 2 {
			return match[1] + "-" + match[2]
		}
		if match := tokyoHotRESKYEX.FindStringSubmatch(norm); len(match) > 1 {
			return match[1]
		}
		if match := normalNoSepPattern.FindStringSubmatch(norm); len(match) > 2 {
			return match[1] + "-" + match[2]
		}
	}

	if match := tmaPattern.FindStringSubmatch(norm); len(match) > 1 {
		return match[1]
	}
	if match := tokyoHotNKPattern.FindStringSubmatch(norm); len(match) > 1 {
		return match[1]
	}
	if match := r18Pattern.FindString(norm); match != "" {
		return match
	}
	if match := numericPattern.FindStringSubmatch(norm); len(match) > 1 {
		return match[1]
	}
	if strings.Contains(norm, ")(") {
		if avid := GetID(strings.ReplaceAll(norm, ")(", "-"), ignoredPatterns); avid != "" {
			return avid
		}
	}

	parent := filepath.Base(filepath.Dir(path))
	if parent != "." && parent != string(filepath.Separator) && parent != "" {
		return GetID(parent, ignoredPatterns)
	}
	return ""
}

func GetCID(path string) string {
	base := filepath.Base(path)
	basename := strings.TrimSuffix(base, filepath.Ext(base))
	possible := cdPostfixPattern.ReplaceAllString(basename, "")
	if !regexp.MustCompile(`^[a-z\d_]+$`).MatchString(possible) {
		return ""
	}
	if !strings.Contains(possible, "_") {
		if regexp.MustCompile(`^[a-z\d]{7,19}$`).MatchString(possible) {
			return possible
		}
		return ""
	}

	pattern := regexp.MustCompile(`^h_\d{3,4}[a-z]{1,10}\d{2,5}[a-z\d]{0,8}$|^\d{3}_\d{4,5}$|^402[a-z]{3,6}\d*_[a-z]{3,8}\d{5,6}$|^h_\d{3,4}wvr\d\w\d{4,5}[a-z\d]{0,8}$`)
	if pattern.MatchString(possible) {
		return possible
	}
	return ""
}

func GuessAVType(avid string) model.DataSource {
	if regexp.MustCompile(`(?i)^FC2-\d{5,7}$`).MatchString(avid) {
		return model.DataSourceFC2
	}
	if regexp.MustCompile(`(?i)^GETCHU-(\d+)`).MatchString(avid) {
		return model.DataSourceGetchu
	}
	if regexp.MustCompile(`(?i)^GYUTTO-(\d+)`).MatchString(avid) {
		return model.DataSourceGyutto
	}
	if cid := GetCID(avid); cid == avid {
		return model.DataSourceCID
	}
	return model.DataSourceNormal
}
