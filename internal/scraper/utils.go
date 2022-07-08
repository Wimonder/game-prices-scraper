package scraper

import (
	"fmt"
	"net/url"
)

func resolveRedirectURL(redirectToVendor string) string {
	res, _ := client.Get(redirectToVendor)
	vendorLocation := res.Header.Get("Location")
	parsedVendorLocation, err := url.Parse(vendorLocation)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%v://%v%v", parsedVendorLocation.Scheme, parsedVendorLocation.Host, parsedVendorLocation.Path)
}
