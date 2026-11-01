package analytics

import (
	"fmt"
	"html/template"
	"regexp"
)

var gaID = regexp.MustCompile(`^G-[A-Z0-9]{4,20}$`)

func (c Config) validateGA() error {
	if c.GAMeasurementID != "" && !gaID.MatchString(c.GAMeasurementID) {
		return fmt.Errorf("analytics: GA_MEASUREMENT_ID %q is not a GA4 measurement ID (G-XXXXXXX)",
			c.GAMeasurementID)
	}

	return nil
}

// gaBootstrap sets the Consent Mode v2 defaults and queues the config, then
// injects gtag.js only once consent is granted (basic consent mode: Google
// gets nothing before that). It sends page_view itself, on load and on
// every Inertia navigation, because send_page_view is off. %s is the
// quoted, JS-escaped measurement ID, %t whether consent starts granted.
const gaBootstrap = `window.dataLayer=window.dataLayer||[];function gtag(){dataLayer.push(arguments);}
(function(id,granted){var keys=["analytics_storage","ad_storage","ad_user_data","ad_personalization"];
function consent(v){var o={};keys.forEach(function(k){o[k]=v;});return o;}
gtag("consent","default",consent(granted?"granted":"denied"));gtag("js",new Date());gtag("config",id,{send_page_view:false});
var nonce=document.currentScript&&document.currentScript.nonce,loaded=false,last;
function pageView(){if(!loaded||location.href===last)return;last=location.href;gtag("event","page_view",{page_location:location.href,page_title:document.title});}
function load(){if(loaded)return;loaded=true;var s=document.createElement("script");s.async=true;s.src="https://www.googletagmanager.com/gtag/js?id="+encodeURIComponent(id);if(nonce)s.nonce=nonce;document.head.appendChild(s);pageView();}
window.vukemAnalytics={consent:function(c){var u=typeof c==="object"&&c?c:consent(c?"granted":"denied");gtag("consent","update",u);for(var k in u){if(u[k]==="granted"){load();break;}}}};
document.addEventListener("inertia:navigate",pageView);if(granted)load();
})(%s,%t);`

func (c Config) gaTag(nonce string) string {
	if c.GAMeasurementID == "" {
		return ""
	}

	id := `"` + template.JSEscapeString(c.GAMeasurementID) + `"` // escapes <, > and quotes too

	return `<script` + nonceAttr(nonce) + `>` + fmt.Sprintf(gaBootstrap, id, c.GAConsentDefaultGranted) + `</script>`
}

// gaCSP lists the origins of Google's GA4 CSP guide
// (https://developers.google.com/tag-platform/security/guides/csp).
func (c Config) gaCSP() map[string][]string {
	if c.GAMeasurementID == "" {
		return nil
	}

	return map[string][]string{
		"script-src": {"https://www.googletagmanager.com"},
		"connect-src": {
			"https://*.google-analytics.com", "https://*.analytics.google.com", "https://www.googletagmanager.com",
		},
		"img-src": {"https://*.google-analytics.com", "https://www.googletagmanager.com"},
	}
}
