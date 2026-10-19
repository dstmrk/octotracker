package checker

// Port 1:1 di tests/test_checker.py: ogni test Python ha un test Go con lo
// stesso nome (in CamelCase) e gli stessi scenari/asserzioni. Dove il test
// Python patcha funzioni del modulo (checker.load_users, checker.Bot, ...)
// qui si usano fakeStore e il FakeClient di testutil.

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"strings"
	"testing"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/formatters"
	"github.com/dstmrk/octotracker/internal/testutil"
	"github.com/dstmrk/octotracker/internal/tg"
)

// test_complete_match_no_savings
func TestCompleteMatchNoSavings(t *testing.T) {
	user := userWith("1", utility("fissa", "monoraria", 0.145, 72.0), gasPtr(utility("fissa", "monoraria", 0.456, 84.0)))
	current := merge(
		tariff("luce", "fissa", "monoraria", rate(0.145, 72.0)),
		tariff("gas", "fissa", "monoraria", rate(0.456, 84.0)),
	)

	savings := CheckBetterRates(user, current)

	if savings.HasSavings {
		t.Error("has_savings dovrebbe essere False")
	}
	if savings.Luce.Energia != nil || savings.Luce.Comm != nil || savings.Gas.Energia != nil || savings.Gas.Comm != nil {
		t.Errorf("nessun risparmio atteso: %+v", savings)
	}
}

// test_luce_energy_savings
func TestLuceEnergySavings(t *testing.T) {
	user := userWith("1", utility("fissa", "monoraria", 0.145, 72.0), nil)
	current := merge(tariff("luce", "fissa", "monoraria", rate(0.130, 72.0)), database.CurrentRates{"gas": {}})

	savings := CheckBetterRates(user, current)

	if !savings.HasSavings {
		t.Fatal("has_savings dovrebbe essere True")
	}
	if savings.Luce.Energia == nil {
		t.Fatal("luce_energia atteso")
	}
	if savings.Luce.Energia.Attuale != 0.145 || savings.Luce.Energia.Nuova != 0.130 {
		t.Errorf("luce_energia = %+v", *savings.Luce.Energia)
	}
	if math.Abs(savings.Luce.Energia.Risparmio-0.015) >= 0.0001 {
		t.Errorf("risparmio = %v", savings.Luce.Energia.Risparmio)
	}
}

// test_mixed_luce_better_worse
func TestMixedLuceBetterWorse(t *testing.T) {
	user := userWith("1", utility("variabile", "monoraria", 0.010, 60.0), nil)
	current := merge(tariff("luce", "variabile", "monoraria", rate(0.0088, 72.0)), database.CurrentRates{"gas": {}})

	savings := CheckBetterRates(user, current)

	if !savings.HasSavings || savings.Luce.Energia == nil || !savings.Luce.CommWorse || !savings.IsMixed {
		t.Errorf("atteso caso mixed: %+v", savings)
	}
}

// test_no_cross_type_comparison_fissa_vs_variabile
func TestNoCrossTypeComparisonFissaVsVariabile(t *testing.T) {
	user := userWith("1", utility("fissa", "monoraria", 0.145, 72.0), nil)
	current := database.CurrentRates{
		"luce": {
			"fissa":     {}, // Vuoto
			"variabile": {"monoraria": rate(0.0088, 72.0)},
		},
		"gas": {},
	}

	savings := CheckBetterRates(user, current)

	// Non deve confrontare fissa con variabile
	if savings.HasSavings || savings.Luce.Energia != nil {
		t.Errorf("nessun confronto atteso: %+v", savings)
	}
}

// test_no_cross_fascia_comparison_mono_vs_tri
func TestNoCrossFasciaComparisonMonoVsTri(t *testing.T) {
	user := userWith("1", utility("variabile", "monoraria", 0.010, 72.0), nil)
	// In Python "monoraria": {} è un dict vuoto (falsy, come una tariffa
	// assente): get_current_rates non produce mai tariffe vuote, quindi qui
	// è semplicemente assente.
	current := database.CurrentRates{
		"luce": {"variabile": {"trioraria": rate(0.0088, 72.0)}},
		"gas":  {},
	}

	savings := CheckBetterRates(user, current)

	// Non deve confrontare monoraria con trioraria
	if savings.HasSavings || savings.Luce.Energia != nil {
		t.Errorf("nessun confronto atteso: %+v", savings)
	}
}

// test_user_with_gas_partial_current_rates
func TestUserWithGasPartialCurrentRates(t *testing.T) {
	user := userWith("1", utility("fissa", "monoraria", 0.145, 72.0), gasPtr(utility("fissa", "monoraria", 0.456, 84.0)))
	current := database.CurrentRates{
		"luce": {"fissa": {"monoraria": rate(0.130, 72.0)}},
		"gas":  {"fissa": {}, "variabile": {}}, // Gas non disponibile
	}

	savings := CheckBetterRates(user, current)

	// Deve trovare risparmio luce
	if !savings.HasSavings || savings.Luce.Energia == nil {
		t.Errorf("risparmio luce atteso: %+v", savings)
	}
	// Gas non confrontato (non disponibile)
	if savings.Gas.Energia != nil || savings.Gas.Comm != nil {
		t.Errorf("gas non doveva essere confrontato: %+v", savings.Gas)
	}
}

// test_user_without_gas
func TestUserWithoutGas(t *testing.T) {
	user := userWith("1", utility("variabile", "trioraria", 0.010, 72.0), nil)
	current := merge(
		tariff("luce", "variabile", "trioraria", rate(0.0088, 60.0)),
		tariff("gas", "fissa", "monoraria", rate(0.456, 84.0)),
	)

	savings := CheckBetterRates(user, current)

	// Deve trovare risparmio luce
	if !savings.HasSavings || savings.Luce.Energia == nil || savings.Luce.Comm == nil {
		t.Errorf("risparmio luce atteso: %+v", savings)
	}
	// Gas non deve essere confrontato
	if savings.Gas.Energia != nil || savings.Gas.Comm != nil {
		t.Errorf("gas non doveva essere confrontato: %+v", savings.Gas)
	}
}

// test_empty_current_rates
func TestEmptyCurrentRates(t *testing.T) {
	user := userWith("1", utility("fissa", "monoraria", 0.145, 72.0), nil)
	current := database.CurrentRates{
		"luce": {"fissa": {}, "variabile": {}},
		"gas":  {"fissa": {}, "variabile": {}},
	}

	savings := CheckBetterRates(user, current)

	// Nessun risparmio trovato (niente da confrontare)
	if savings.HasSavings || savings.Luce.Energia != nil || savings.Luce.Comm != nil {
		t.Errorf("nessun risparmio atteso: %+v", savings)
	}
}

// test_both_luce_and_gas_savings
func TestBothLuceAndGasSavings(t *testing.T) {
	user := userWith("1", utility("fissa", "monoraria", 0.145, 72.0), gasPtr(utility("variabile", "monoraria", 0.10, 90.0)))
	current := merge(
		tariff("luce", "fissa", "monoraria", rate(0.130, 60.0)),
		tariff("gas", "variabile", "monoraria", rate(0.08, 84.0)),
	)

	savings := CheckBetterRates(user, current)

	// Risparmio su tutto
	if !savings.HasSavings || savings.Luce.Energia == nil || savings.Luce.Comm == nil ||
		savings.Gas.Energia == nil || savings.Gas.Comm == nil {
		t.Errorf("risparmio su tutto atteso: %+v", savings)
	}
}

// test_tipo_and_fascia_in_savings
func TestTipoAndFasciaInSavings(t *testing.T) {
	user := userWith("1", utility("variabile", "trioraria", 0.010, 72.0), gasPtr(utility("fissa", "monoraria", 0.456, 84.0)))
	current := merge(
		tariff("luce", "variabile", "trioraria", rate(0.0088, 72.0)),
		tariff("gas", "fissa", "monoraria", rate(0.456, 84.0)),
	)

	savings := CheckBetterRates(user, current)

	// Verifica tipo e fascia memorizzati correttamente
	if savings.Luce.Tipo != "variabile" || savings.Luce.Fascia != "trioraria" ||
		savings.Gas.Tipo != "fissa" || savings.Gas.Fascia != "monoraria" {
		t.Errorf("tipo/fascia errati: %+v", savings)
	}
}

// utilityKeys restituisce le chiavi JSON di una fornitura (il dict Python).
func utilityKeys(t *testing.T, u database.Utility) map[string]bool {
	t.Helper()
	m := map[string]any{}
	b, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for k := range m {
		keys[k] = true
	}
	return keys
}

func sameKeys(got map[string]bool, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for _, k := range want {
		if !got[k] {
			return false
		}
	}
	return true
}

// test_user_with_gas_no_notifications
func TestUserWithGasNoNotifications(t *testing.T) {
	user := userWith("1", utility("fissa", "monoraria", 0.145, 72.0), gasPtr(utility("fissa", "monoraria", 0.456, 84.0)))

	// Verifica struttura base
	if user.Gas == nil {
		t.Fatal("gas atteso")
	}
	// Verifica campi luce e gas (stesse chiavi del dict Python)
	if k := utilityKeys(t, user.Luce); !sameKeys(k, "tipo", "fascia", "energia", "commercializzazione") {
		t.Errorf("chiavi luce = %v", k)
	}
	if k := utilityKeys(t, *user.Gas); !sameKeys(k, "tipo", "fascia", "energia", "commercializzazione") {
		t.Errorf("chiavi gas = %v", k)
	}
	if len(user.LastNotifiedRates) != 0 {
		t.Error("nessuna notifica precedente attesa")
	}
}

// test_user_without_gas_no_notifications
func TestUserWithoutGasNoNotifications(t *testing.T) {
	user := userWith("1", utility("variabile", "monoraria", 0.0088, 72.0), nil)

	// Verifica struttura: gas None
	if user.Gas != nil {
		t.Error("gas dovrebbe essere nil (None)")
	}
	if userUtility(user, "gas") != nil {
		t.Error("user_rates.get('gas') dovrebbe essere None")
	}
}

// test_user_with_last_notified_rates
func TestUserWithLastNotifiedRates(t *testing.T) {
	user := userWith("1", utility("variabile", "monoraria", 0.010, 72.0), gasPtr(utility("variabile", "monoraria", 0.10, 84.0)))
	user.LastNotifiedRates = database.LastNotifiedRates{
		"luce": database.NotifiedRate(0.0088, 72.0),
		"gas":  database.NotifiedRate(0.08, 84.0),
	}

	// Verifica struttura last_notified_rates
	luce, ok := user.LastNotifiedRates["luce"].(map[string]any)
	if !ok {
		t.Fatal("luce attesa in last_notified_rates")
	}
	if _, ok := user.LastNotifiedRates["gas"]; !ok {
		t.Error("gas atteso in last_notified_rates")
	}
	// Verifica che last_notified non contenga tipo/fascia (ridondanti)
	if _, ok := luce["tipo"]; ok {
		t.Error("tipo non atteso")
	}
	if _, ok := luce["fascia"]; ok {
		t.Error("fascia non attesa")
	}
	// Verifica campi corretti
	keys := map[string]bool{}
	for k := range luce {
		keys[k] = true
	}
	if !sameKeys(keys, "energia", "commercializzazione") {
		t.Errorf("chiavi = %v", keys)
	}
}

// test_user_without_gas_with_last_notified
func TestUserWithoutGasWithLastNotified(t *testing.T) {
	user := userWith("1", utility("fissa", "monoraria", 0.145, 72.0), nil)
	user.LastNotifiedRates = database.LastNotifiedRates{"luce": database.NotifiedRate(0.130, 60.0)}

	// last_notified_rates può avere solo luce
	if _, ok := user.LastNotifiedRates["luce"]; !ok {
		t.Error("luce attesa")
	}
	if _, ok := user.LastNotifiedRates["gas"]; ok {
		t.Error("gas non atteso")
	}
}

// ========== TEST HELPER FUNCTIONS ==========

// test_compare_rate_field_improvement
func TestCompareRateFieldImprovement(t *testing.T) {
	saving, isWorse := compareRateField(0.145, fp(0.130))

	if saving == nil {
		t.Fatal("saving atteso")
	}
	if saving.Attuale != 0.145 || saving.Nuova != 0.130 || math.Abs(saving.Risparmio-0.015) >= 0.0001 {
		t.Errorf("saving = %+v", *saving)
	}
	if isWorse {
		t.Error("is_worse dovrebbe essere False")
	}
}

// test_compare_rate_field_worsening
func TestCompareRateFieldWorsening(t *testing.T) {
	saving, isWorse := compareRateField(0.130, fp(0.145))

	if saving != nil || !isWorse {
		t.Errorf("atteso (None, True), ottenuto (%v, %v)", saving, isWorse)
	}
}

// test_compare_rate_field_no_change
func TestCompareRateFieldNoChange(t *testing.T) {
	saving, isWorse := compareRateField(0.145, fp(0.145))

	if saving != nil || isWorse {
		t.Errorf("atteso (None, False), ottenuto (%v, %v)", saving, isWorse)
	}
}

// test_compare_rate_field_none_value
func TestCompareRateFieldNoneValue(t *testing.T) {
	saving, isWorse := compareRateField(0.145, nil)

	if saving != nil || isWorse {
		t.Errorf("atteso (None, False), ottenuto (%v, %v)", saving, isWorse)
	}
}

// test_check_utility_rates_with_savings
func TestCheckUtilityRatesWithSavings(t *testing.T) {
	userUtility := utility("fissa", "monoraria", 0.145, 72.0)
	current := tariff("luce", "fissa", "monoraria", rate(0.130, 60.0))

	result := checkUtilityRates(userUtility, current, "luce")

	if !result.hasSavings || result.energiaSaving == nil || result.commSaving == nil || result.energiaWorse || result.commWorse {
		t.Errorf("risultato = %+v", result)
	}
}

// test_check_utility_rates_no_rate_available
func TestCheckUtilityRatesNoRateAvailable(t *testing.T) {
	userUtility := utility("fissa", "monoraria", 0.145, 72.0)
	current := database.CurrentRates{"luce": {"fissa": {}}} // Nessuna monoraria

	result := checkUtilityRates(userUtility, current, "luce")

	if result.hasSavings || result.energiaSaving != nil || result.commSaving != nil {
		t.Errorf("risultato = %+v", result)
	}
}

// test_build_current_octopus_rates_with_luce_only
func TestBuildCurrentOctopusRatesWithLuceOnly(t *testing.T) {
	user := userWith("1", utility("fissa", "monoraria", 0.145, 72.0), nil)
	current := merge(tariff("luce", "fissa", "monoraria", rate(0.130, 60.0)), database.CurrentRates{"gas": {}})

	result := buildCurrentOctopusRates(user, current)

	luce, ok := result["luce"].(map[string]any)
	if !ok {
		t.Fatalf("luce attesa: %v", result)
	}
	if luce["energia"] != 0.130 || luce["commercializzazione"] != 60.0 {
		t.Errorf("luce = %v", luce)
	}
	if _, ok := result["gas"]; ok {
		t.Error("gas non atteso")
	}
}

// test_build_current_octopus_rates_with_luce_and_gas
func TestBuildCurrentOctopusRatesWithLuceAndGas(t *testing.T) {
	user := userWith("1", utility("fissa", "monoraria", 0.145, 72.0), gasPtr(utility("variabile", "monoraria", 0.10, 84.0)))
	current := merge(
		tariff("luce", "fissa", "monoraria", rate(0.130, 60.0)),
		tariff("gas", "variabile", "monoraria", rate(0.08, 78.0)),
	)

	result := buildCurrentOctopusRates(user, current)

	luce, okL := result["luce"].(map[string]any)
	gas, okG := result["gas"].(map[string]any)
	if !okL || !okG {
		t.Fatalf("luce e gas attesi: %v", result)
	}
	if luce["energia"] != 0.130 || gas["energia"] != 0.08 {
		t.Errorf("result = %v", result)
	}
}

// test_should_notify_user_first_notification
func TestShouldNotifyUserFirstNotification(t *testing.T) {
	user := &database.User{ID: "1", Luce: database.Utility{Tipo: "fissa", Fascia: "monoraria"}}
	currentOctopus := database.LastNotifiedRates{"luce": database.NotifiedRate(0.130, 60.0)}

	if !shouldNotifyUser(user, currentOctopus) {
		t.Error("prima notifica: atteso True")
	}
}

// test_should_notify_user_rates_changed
func TestShouldNotifyUserRatesChanged(t *testing.T) {
	user := &database.User{
		ID:                "1",
		Luce:              database.Utility{Tipo: "fissa", Fascia: "monoraria"},
		LastNotifiedRates: database.LastNotifiedRates{"luce": database.NotifiedRate(0.135, 65.0)},
	}
	currentOctopus := database.LastNotifiedRates{"luce": database.NotifiedRate(0.130, 60.0)}

	if !shouldNotifyUser(user, currentOctopus) {
		t.Error("tariffe cambiate: atteso True")
	}
}

// test_should_notify_user_already_notified
func TestShouldNotifyUserAlreadyNotified(t *testing.T) {
	user := &database.User{
		ID:                "1",
		Luce:              database.Utility{Tipo: "fissa", Fascia: "monoraria"},
		LastNotifiedRates: database.LastNotifiedRates{"luce": database.NotifiedRate(0.130, 60.0)},
	}
	currentOctopus := database.LastNotifiedRates{"luce": database.NotifiedRate(0.130, 60.0)}

	if shouldNotifyUser(user, currentOctopus) {
		t.Error("già notificato: atteso False")
	}
}

// test_should_notify_user_after_user_rate_update_with_lower_octopus_rates
func TestShouldNotifyUserAfterUserRateUpdateWithLowerOctopusRates(t *testing.T) {
	user := userWith("1", utility("fissa", "monoraria", 0.130, 60.0), nil)
	user.LastNotifiedRates = database.LastNotifiedRates{"luce": database.NotifiedRate(0.130, 60.0)}
	currentOctopus := database.LastNotifiedRates{"luce": database.NotifiedRate(0.120, 58.0)}

	if !shouldNotifyUser(user, currentOctopus) {
		t.Error("Octopus scende ancora: atteso True")
	}
}

// ========== TESTS FOR FORMATTING FUNCTIONS ==========

// test_format_number_integer (checker.py riesporta format_number da formatters)
func TestFormatNumberInteger(t *testing.T) {
	if got := formatters.FormatNumber(72.0, 2); got != "72" {
		t.Errorf("got %q", got)
	}
}

// test_format_number_with_decimals
func TestFormatNumberWithDecimals(t *testing.T) {
	if got := formatters.FormatNumber(0.1078, 4); got != "0,1078" {
		t.Errorf("got %q", got)
	}
}

// test_format_number_trailing_zeros
func TestFormatNumberTrailingZeros(t *testing.T) {
	if got := formatters.FormatNumber(0.1000, 4); got != "0,10" {
		t.Errorf("got %q", got)
	}
}

// test_format_number_two_decimals_min
func TestFormatNumberTwoDecimalsMin(t *testing.T) {
	if got := formatters.FormatNumber(0.5, 4); got != "0,50" {
		t.Errorf("got %q", got)
	}
}

// test_format_header_mixed
func TestFormatHeaderMixed(t *testing.T) {
	result := formatHeader(true)

	assertContains(t, result, "⚖️")
	assertContains(t, result, "Aggiornamento tariffe")
}

// test_format_header_savings
func TestFormatHeaderSavings(t *testing.T) {
	result := formatHeader(false)

	assertContains(t, result, "⚡️")
	assertContains(t, result, "Buone notizie")
}

// test_format_luce_section_with_savings
func TestFormatLuceSectionWithSavings(t *testing.T) {
	savings := Savings{Luce: UtilitySavings{
		Tipo:    "fissa",
		Fascia:  "monoraria",
		Energia: &RateSaving{Attuale: 0.145, Nuova: 0.130, Risparmio: 0.015},
	}}
	user := userWith("1", utility("fissa", "monoraria", 0.145, 72.0), nil)
	current := tariff("luce", "fissa", "monoraria", rate(0.130, 72.0))

	result := formatLuceSection(savings, user, current, nil)

	assertContains(t, result, "💡")
	assertContains(t, result, "Luce")
	assertContains(t, result, "0,145")
}

// test_format_luce_section_no_savings
func TestFormatLuceSectionNoSavings(t *testing.T) {
	savings := Savings{Luce: UtilitySavings{Tipo: "fissa", Fascia: "monoraria"}}
	user := &database.User{ID: "1", Luce: database.Utility{Tipo: "fissa", Fascia: "monoraria"}}

	if result := formatLuceSection(savings, user, database.CurrentRates{}, nil); result != "" {
		t.Errorf("atteso \"\", ottenuto %q", result)
	}
}

// test_format_gas_section_with_savings
func TestFormatGasSectionWithSavings(t *testing.T) {
	savings := Savings{Gas: UtilitySavings{
		Tipo:    "fissa",
		Fascia:  "monoraria",
		Energia: &RateSaving{Attuale: 0.456, Nuova: 0.400, Risparmio: 0.056},
	}}
	user := &database.User{ID: "1", Gas: gasPtr(utility("fissa", "monoraria", 0.456, 84.0))}
	current := tariff("gas", "fissa", "monoraria", rate(0.400, 84.0))

	result := formatGasSection(savings, user, current, nil)

	assertContains(t, result, "🔥")
	assertContains(t, result, "Gas")
	assertContains(t, result, "0,456")
}

// test_format_gas_section_no_gas
func TestFormatGasSectionNoGas(t *testing.T) {
	user := &database.User{ID: "1"} // "gas": None

	if result := formatGasSection(Savings{}, user, database.CurrentRates{}, nil); result != "" {
		t.Errorf("atteso \"\", ottenuto %q", result)
	}
}

// test_format_footer_mixed
func TestFormatFooterMixed(t *testing.T) {
	result := formatFooter(true, false, nil, nil, true, false)

	assertContains(t, result, "📊")
	assertContains(t, result, "convenienza dipende")
}

// test_format_footer_savings
func TestFormatFooterSavings(t *testing.T) {
	result := formatFooter(false, false, nil, nil, true, true)

	assertContains(t, result, "👇")
	assertContains(t, result, "aggiornare le tariffe")
}

// test_format_notification
func TestFormatNotification(t *testing.T) {
	savings := Savings{Luce: UtilitySavings{
		Tipo:    "fissa",
		Fascia:  "monoraria",
		Energia: &RateSaving{Attuale: 0.145, Nuova: 0.130, Risparmio: 0.015},
	}}
	user := userWith("1", utility("fissa", "monoraria", 0.145, 72.0), nil)
	current := tariff("luce", "fissa", "monoraria", rate(0.130, 72.0))

	result := FormatNotification(savings, user, current, true, true, nil, nil)

	assertContains(t, result, "⚡️")
	assertContains(t, result, "💡")
	assertContains(t, result, "ko-fi.com")
}

// test_format_notification_with_cod_offerta
func TestFormatNotificationWithCodOfferta(t *testing.T) {
	savings := Savings{
		Luce: UtilitySavings{
			Tipo:    "variabile",
			Fascia:  "monoraria",
			Energia: &RateSaving{Attuale: 0.010, Nuova: 0.0088, Risparmio: 0.0012},
		},
		Gas: UtilitySavings{
			Tipo:    "fissa",
			Fascia:  "monoraria",
			Energia: &RateSaving{Attuale: 0.450, Nuova: 0.360, Risparmio: 0.090},
			Comm:    &RateSaving{Attuale: 90.0, Nuova: 84.0, Risparmio: 6.0},
		},
	}
	user := userWith("1", utility("variabile", "monoraria", 0.010, 72.0), gasPtr(utility("fissa", "monoraria", 0.450, 90.0)))
	current := merge(
		tariff("luce", "variabile", "monoraria", rateCod(0.0088, 72.0, "000129ESVML77XXXXXOCTOFLEXMONv77")),
		tariff("gas", "fissa", "monoraria", rateCod(0.360, 84.0, "000129GSFML37XXXXXXXXOCTOFIXGv37")),
	)

	result := FormatNotification(savings, user, current, true, true, nil, nil)

	// Verifica che i codici offerta appaiano nel messaggio
	assertContains(t, result, "📋 Codice offerta:")
	assertContains(t, result, "000129ESVML77XXXXXOCTOFLEXMONv77")
	assertContains(t, result, "000129GSFML37XXXXXXXXOCTOFIXGv37")
	assertContains(t, result, "<code>") // Verifica formato HTML
}

// test_format_notification_without_cod_offerta
func TestFormatNotificationWithoutCodOfferta(t *testing.T) {
	savings := Savings{Luce: UtilitySavings{
		Tipo:    "fissa",
		Fascia:  "monoraria",
		Energia: &RateSaving{Attuale: 0.145, Nuova: 0.130, Risparmio: 0.015},
	}}
	user := userWith("1", utility("fissa", "monoraria", 0.145, 72.0), nil)
	// Tariffe senza cod_offerta
	current := tariff("luce", "fissa", "monoraria", rate(0.130, 72.0))

	result := FormatNotification(savings, user, current, true, true, nil, nil)

	assertContains(t, result, "⚡️")
	assertContains(t, result, "💡")
	// Il codice offerta non dovrebbe apparire
	assertNotContains(t, result, "📋 Codice offerta:")
}

// ========== TESTS FOR ASYNC FUNCTIONS ==========

// test_send_notification_success
func TestSendNotificationSuccess(t *testing.T) {
	b, fc := testutil.NewBot(t)
	store := &fakeStore{}

	result := SendNotification(context.Background(), b, store, "123456", "Test message", nil)

	if !result {
		t.Fatal("atteso True")
	}
	calls := fc.CallsTo("sendMessage")
	if len(calls) != 1 {
		t.Fatalf("attesa 1 chiamata sendMessage, ottenute %d", len(calls))
	}
	c := calls[0]
	if c.ChatID() != "123456" || c.Text() != "Test message" || c.ParseMode() != "HTML" {
		t.Errorf("parametri = %v", c.Params)
	}
	if _, ok := c.Params["reply_markup"]; ok {
		t.Errorf("reply_markup=None non deve essere inviato: %v", c.Params)
	}
}

// sendWithError esegue SendNotification con sendMessage che fallisce con err.
func sendWithError(t *testing.T, err error) (bool, *fakeStore) {
	t.Helper()
	b, fc := testutil.NewBot(t)
	fc.FailMethod("sendMessage", err)
	store := &fakeStore{}
	return SendNotification(context.Background(), b, store, "123456", "Test message", nil), store
}

// timeoutError replica l'errore restituito da gotgbot quando scade il
// timeout della richiesta.
func timeoutError() error {
	return &wrappedErr{msg: "failed to execute POST request to sendMessage", err: &url.Error{
		Op: "Post", URL: "https://api.telegram.org/bot<TOKEN>/sendMessage", Err: context.DeadlineExceeded,
	}}
}

// networkError replica un errore di trasporto di gotgbot.
func networkError(msg string) error {
	return &wrappedErr{msg: "failed to execute POST request to sendMessage", err: &url.Error{
		Op: "Post", URL: "https://api.telegram.org/bot<TOKEN>/sendMessage", Err: errors.New(msg),
	}}
}

type wrappedErr struct {
	msg string
	err error
}

func (w *wrappedErr) Error() string { return w.msg + ": " + w.err.Error() }
func (w *wrappedErr) Unwrap() error { return w.err }

// test_send_notification_retry_after
func TestSendNotificationRetryAfter(t *testing.T) {
	logs := captureLogs(t)

	result, store := sendWithError(t, testutil.RetryAfterError("sendMessage", 10))

	if result {
		t.Error("atteso False")
	}
	if len(store.removedIDs()) != 0 {
		t.Error("l'utente non deve essere rimosso")
	}
	assertContains(t, logs.String(), "WARNING - ⏱️  Rate limit per utente 123456: riprova tra 10s")
}

// test_send_notification_timeout
func TestSendNotificationTimeout(t *testing.T) {
	logs := captureLogs(t)

	result, store := sendWithError(t, timeoutError())

	if result {
		t.Error("atteso False")
	}
	if len(store.removedIDs()) != 0 {
		t.Error("l'utente non deve essere rimosso")
	}
	assertContains(t, logs.String(), "ERROR - ⏱️  Timeout invio messaggio a 123456")
}

// test_send_notification_network_error
func TestSendNotificationNetworkError(t *testing.T) {
	logs := captureLogs(t)

	result, _ := sendWithError(t, networkError("Network error"))

	if result {
		t.Error("atteso False")
	}
	assertContains(t, logs.String(), "ERROR - 🌐 Errore di rete invio messaggio a 123456: failed to execute POST request to sendMessage")
	assertContains(t, logs.String(), "Network error")
}

// test_send_notification_telegram_error
func TestSendNotificationTelegramError(t *testing.T) {
	logs := captureLogs(t)

	// Errore Telegram generico non di rete (es. Conflict, 409).
	result, store := sendWithError(t, testutil.TelegramError("sendMessage", 409, "Generic error"))

	if result {
		t.Error("atteso False")
	}
	if len(store.removedIDs()) != 0 {
		t.Error("l'utente non deve essere rimosso")
	}
	assertContains(t, logs.String(), "ERROR - ❌ Errore Telegram invio messaggio a 123456: Generic error")
}

// assertRemoved verifica remove_user chiamato una volta con "123456".
func assertRemoved(t *testing.T, store *fakeStore) {
	t.Helper()
	if got := store.removedIDs(); len(got) != 1 || got[0] != "123456" {
		t.Errorf("remove_user chiamate = %v, attesa una con \"123456\"", got)
	}
}

// test_send_notification_bot_blocked_removes_user
func TestSendNotificationBotBlockedRemovesUser(t *testing.T) {
	logs := captureLogs(t)

	result, store := sendWithError(t, testutil.TelegramError("sendMessage", 403, "Forbidden: bot was blocked by the user"))

	if result {
		t.Error("atteso False")
	}
	assertRemoved(t, store)
	assertContains(t, logs.String(),
		"WARNING - 🚫 Utente 123456 non raggiungibile ('Forbidden: bot was blocked by the user') - rimozione dal database")
}

// test_send_notification_user_deactivated_removes_user
func TestSendNotificationUserDeactivatedRemovesUser(t *testing.T) {
	result, store := sendWithError(t, testutil.TelegramError("sendMessage", 403, "Forbidden: user is deactivated"))

	if result {
		t.Error("atteso False")
	}
	assertRemoved(t, store)
}

// test_send_notification_bot_kicked_removes_user
func TestSendNotificationBotKickedRemovesUser(t *testing.T) {
	result, store := sendWithError(t, testutil.TelegramError("sendMessage", 403, "Forbidden: bot was kicked"))

	if result {
		t.Error("atteso False")
	}
	assertRemoved(t, store)
}

// test_send_notification_chat_not_found_removes_user
//
// Il test Python solleva un TelegramError generico con messaggio
// "Bad Request: chat not found": qui lo stesso messaggio arriva con un
// codice candidato alla rimozione (403). Un vero HTTP 400 è un errore di
// rete e NON rimuove l'utente (vedi
// TestSendNotificationRealBadRequestChatNotFoundIsNetworkError).
func TestSendNotificationChatNotFoundRemovesUser(t *testing.T) {
	logs := captureLogs(t)

	err := testutil.TelegramError("sendMessage", 403, "Bad Request: chat not found")
	result, store := sendWithError(t, err)

	if result {
		t.Error("atteso False")
	}
	assertRemoved(t, store)
	assertContains(t, logs.String(), "('"+tg.ErrorString(err)+"')")
}

// test_send_notification_case_insensitive_matching
func TestSendNotificationCaseInsensitiveMatching(t *testing.T) {
	// Errore con maiuscole/minuscole diverse
	result, store := sendWithError(t, testutil.TelegramError("sendMessage", 403, "FORBIDDEN: BOT WAS BLOCKED BY THE USER"))

	if result {
		t.Error("atteso False")
	}
	assertRemoved(t, store)
}

// test_send_notification_other_error_does_not_remove_user
func TestSendNotificationOtherErrorDoesNotRemoveUser(t *testing.T) {
	result, store := sendWithError(t, testutil.TelegramError("sendMessage", 403, "Some other error"))

	if result {
		t.Error("atteso False")
	}
	// Verifica che remove_user NON sia stato chiamato
	if got := store.removedIDs(); len(got) != 0 {
		t.Errorf("remove_user non doveva essere chiamato: %v", got)
	}
}

// test_check_and_notify_users_no_users
func TestCheckAndNotifyUsersNoUsers(t *testing.T) {
	b, fc := testutil.NewBot(t)
	store := &fakeStore{users: nil, rates: database.CurrentRates{"luce": {}, "gas": {}}}

	// Non dovrebbe generare errori
	if err := CheckAndNotifyUsers(context.Background(), b, store); err != nil {
		t.Fatalf("errore inatteso: %v", err)
	}
	if n := len(fc.Calls()); n != 0 {
		t.Errorf("nessuna chiamata attesa, ottenute %d", n)
	}
}

// test_check_and_notify_users_no_rates
func TestCheckAndNotifyUsersNoRates(t *testing.T) {
	b, fc := testutil.NewBot(t)
	store := &fakeStore{
		users: []*database.User{{ID: "123", Luce: database.Utility{Tipo: "fissa", Fascia: "monoraria"}}},
		rates: nil,
	}

	// Non dovrebbe generare errori
	if err := CheckAndNotifyUsers(context.Background(), b, store); err != nil {
		t.Fatalf("errore inatteso: %v", err)
	}
	if n := len(fc.Calls()); n != 0 {
		t.Errorf("nessuna chiamata attesa, ottenute %d", n)
	}
}

// test_check_and_notify_users_with_savings
func TestCheckAndNotifyUsersWithSavings(t *testing.T) {
	b, fc := testutil.NewBot(t)
	store := &fakeStore{
		users: []*database.User{userWith("123", utility("fissa", "monoraria", 0.145, 72.0), nil)},
		rates: database.CurrentRates{"luce": {"fissa": {"monoraria": rate(0.130, 72.0)}}, "gas": {}},
	}

	if err := CheckAndNotifyUsers(context.Background(), b, store); err != nil {
		t.Fatalf("errore inatteso: %v", err)
	}

	// Verifica che il messaggio sia stato inviato
	if n := len(fc.CallsTo("sendMessage")); n != 1 {
		t.Errorf("attesa 1 sendMessage, ottenute %d", n)
	}
	// Verifica che l'utente sia stato aggiornato
	if n := len(store.saved); n != 1 {
		t.Errorf("atteso 1 save_user, ottenuti %d", n)
	}
	// Verifica che le tariffe pendenti siano state salvate
	if n := len(store.pending); n != 1 {
		t.Errorf("atteso 1 save_pending_rates, ottenuti %d", n)
	}
}

// test_check_and_notify_users_already_notified
func TestCheckAndNotifyUsersAlreadyNotified(t *testing.T) {
	b, fc := testutil.NewBot(t)
	user := userWith("123", utility("fissa", "monoraria", 0.145, 72.0), nil)
	user.LastNotifiedRates = database.LastNotifiedRates{"luce": database.NotifiedRate(0.130, 72.0)}
	store := &fakeStore{
		users: []*database.User{user},
		rates: database.CurrentRates{"luce": {"fissa": {"monoraria": rate(0.130, 72.0)}}, "gas": {}},
	}

	if err := CheckAndNotifyUsers(context.Background(), b, store); err != nil {
		t.Fatalf("errore inatteso: %v", err)
	}

	// Verifica che nessun messaggio sia stato inviato
	if n := len(fc.CallsTo("sendMessage")); n != 0 {
		t.Errorf("nessun sendMessage atteso, ottenuti %d", n)
	}
}

// test_format_luce_section_worse
func TestFormatLuceSectionWorse(t *testing.T) {
	savings := Savings{Luce: UtilitySavings{
		Tipo:         "fissa",
		Fascia:       "monoraria",
		Comm:         &RateSaving{Attuale: 60.0, Nuova: 72.0, Risparmio: -12.0},
		EnergiaWorse: true,
	}}
	user := userWith("1", utility("fissa", "monoraria", 0.145, 60.0), nil)
	current := tariff("luce", "fissa", "monoraria", rate(0.160, 72.0))

	result := formatLuceSection(savings, user, current, nil)

	// Dovrebbe contenere markup di sottolineatura per peggioramento
	if !strings.Contains(result, "<u>") && result != "" {
		t.Errorf("atteso <u> o \"\": %q", result)
	}
	assertContains(t, result, "<u>0,16 €/kWh</u>")
}

// ========== TEST CONSUMPTION-BASED SAVINGS CALCULATION ==========

func assertApprox(t *testing.T, got *float64, want, abs float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("atteso %v, ottenuto None", want)
	}
	if math.Abs(*got-want) > abs {
		t.Errorf("atteso %v ± %v, ottenuto %v", want, abs, *got)
	}
}

// test_calculate_estimated_savings_monoraria_luce_only
func TestCalculateEstimatedSavingsMonorariaLuceOnly(t *testing.T) {
	luce := utility("fissa", "monoraria", 0.145, 72.0)
	luce.ConsumoF1 = fp(2700.0) // kWh/anno
	user := userWith("1", luce, nil)
	current := tariff("luce", "fissa", "monoraria", rate(0.130, 65.0))

	risparmio := calculateUtilitySavings("luce", user, current)

	// Risparmio = (0.145 - 0.130) * 2700 + (72 - 65) = 40.5 + 7 = 47.5
	assertApprox(t, risparmio, 47.5, 0.1)
}

// test_calculate_estimated_savings_trioraria
func TestCalculateEstimatedSavingsTrioraria(t *testing.T) {
	luce := utility("variabile", "trioraria", 0.025, 72.0)
	luce.ConsumoF1, luce.ConsumoF2, luce.ConsumoF3 = fp(900.0), fp(850.0), fp(950.0)
	user := userWith("1", luce, nil)
	current := tariff("luce", "variabile", "trioraria", rate(0.020, 85.0))

	risparmio := calculateUtilitySavings("luce", user, current)

	// Risparmio energia = 0.005 * 2700 = 13.5; aumento comm = 72 - 85 = -13; totale 0.5
	assertApprox(t, risparmio, 0.5, 0.1)
}

// test_calculate_estimated_savings_with_gas
func TestCalculateEstimatedSavingsWithGas(t *testing.T) {
	luce := utility("fissa", "monoraria", 0.145, 72.0)
	luce.ConsumoF1 = fp(2700.0)
	gas := utility("fissa", "monoraria", 0.456, 84.0)
	gas.ConsumoAnnuo = fp(1200.0)
	user := userWith("1", luce, &gas)
	current := merge(
		tariff("luce", "fissa", "monoraria", rate(0.130, 65.0)),
		tariff("gas", "fissa", "monoraria", rate(0.420, 80.0)),
	)

	// Calcola separatamente per luce e gas
	// Luce: (0.145-0.130)*2700 + (72-65) = 47.5
	assertApprox(t, calculateUtilitySavings("luce", user, current), 47.5, 0.1)
	// Gas: (0.456-0.420)*1200 + (84-80) = 47.2
	assertApprox(t, calculateUtilitySavings("gas", user, current), 47.2, 0.1)
}

// test_calculate_estimated_savings_negative
func TestCalculateEstimatedSavingsNegative(t *testing.T) {
	luce := utility("fissa", "monoraria", 0.130, 65.0)
	luce.ConsumoF1 = fp(2700.0)
	user := userWith("1", luce, nil)
	current := tariff("luce", "fissa", "monoraria", rate(0.145, 72.0))

	// Risparmio = (0.130-0.145)*2700 + (65-72) = -47.5
	assertApprox(t, calculateUtilitySavings("luce", user, current), -47.5, 0.1)
}

// test_calculate_estimated_savings_no_consumption
func TestCalculateEstimatedSavingsNoConsumption(t *testing.T) {
	user := userWith("1", utility("fissa", "monoraria", 0.145, 72.0), nil)
	current := tariff("luce", "fissa", "monoraria", rate(0.130, 65.0))

	if risparmio := calculateUtilitySavings("luce", user, current); risparmio != nil {
		t.Errorf("atteso None, ottenuto %v", *risparmio)
	}
}

// ========== TEST FOOTER WITH CONSUMPTION ==========

// test_format_footer_mixed_without_consumption
func TestFormatFooterMixedWithoutConsumption(t *testing.T) {
	footer := formatFooter(true, false, nil, nil, true, false)

	assertContains(t, footer, "📊 In questi casi la convenienza dipende dai tuoi consumi")
	assertContains(t, footer, "Se vuoi una stima più precisa")
	assertContains(t, footer, "/update")
}

// test_format_footer_mixed_with_consumption
func TestFormatFooterMixedWithConsumption(t *testing.T) {
	footer := formatFooter(true, false, fp(47.5), nil, true, false)

	// Le stime sono ora inline nelle sezioni, quindi non devono apparire nel footer
	assertNotContains(t, footer, "💰 In base ai tuoi consumi")
	assertNotContains(t, footer, "📊 In questi casi") // Non deve apparire il messaggio generico
}

// test_format_footer_not_mixed
func TestFormatFooterNotMixed(t *testing.T) {
	footer := formatFooter(false, false, nil, nil, true, true)

	assertNotContains(t, footer, "📊")
	assertNotContains(t, footer, "💰")
	assertNotContains(t, footer, "consumi")
	assertContains(t, footer, "👇 Vuoi aggiornare le tariffe")
}

// ========== TEST CHECK_AND_NOTIFY SKIP MIXED WITH NEGATIVE SAVINGS ==========

// runCheck esegue CheckAndNotifyUsers su un solo utente "123" e restituisce
// i testi inviati.
func runCheck(t *testing.T, user *database.User, current database.CurrentRates) ([]string, *fakeStore) {
	t.Helper()
	b, fc := testutil.NewBot(t)
	store := &fakeStore{users: []*database.User{user}, rates: current}
	if err := CheckAndNotifyUsers(context.Background(), b, store); err != nil {
		t.Fatalf("errore inatteso: %v", err)
	}
	var texts []string
	for _, c := range fc.CallsTo("sendMessage") {
		texts = append(texts, c.Text())
	}
	return texts, store
}

// singleText verifica che sia stata inviata esattamente una notifica.
func singleText(t *testing.T, texts []string) string {
	t.Helper()
	if len(texts) != 1 {
		t.Fatalf("attesa 1 notifica, ottenute %d", len(texts))
	}
	return texts[0]
}

// test_check_and_notify_skip_mixed_negative_savings
func TestCheckAndNotifySkipMixedNegativeSavings(t *testing.T) {
	luce := utility("fissa", "monoraria", 0.130, 65.0) // Tariffa attuale bassa
	luce.ConsumoF1 = fp(2700.0)
	// Nuove tariffe MIXED: energia migliora di 0.005, comm peggiora di 20
	// Totale: 13.5 - 20 = -6.5 (negativo, deve skippare)
	current := database.CurrentRates{"luce": {"fissa": {"monoraria": rate(0.125, 85.0)}}}

	texts, _ := runCheck(t, userWith("123", luce, nil), current)

	// Verifica che NON sia stata inviata alcuna notifica
	if len(texts) != 0 {
		t.Errorf("nessuna notifica attesa, ottenute %d", len(texts))
	}
}

// test_check_and_notify_send_mixed_positive_savings
func TestCheckAndNotifySendMixedPositiveSavings(t *testing.T) {
	luce := utility("fissa", "monoraria", 0.145, 72.0) // Tariffa attuale alta
	luce.ConsumoF1 = fp(2700.0)
	// Energia migliora di 0.015, comm peggiora di 13 → 40.5 - 13 = 27.5
	current := database.CurrentRates{"luce": {"fissa": {"monoraria": rate(0.130, 85.0)}}}

	texts, _ := runCheck(t, userWith("123", luce, nil), current)

	messageText := singleText(t, texts)
	// Verifica che il messaggio contenga la stima
	assertContains(t, messageText, "💰 In base ai tuoi consumi di luce")
	assertContains(t, messageText, "27,50 €/anno")
}

// test_check_and_notify_both_utilities_non_mixed
func TestCheckAndNotifyBothUtilitiesNonMixed(t *testing.T) {
	user := userWith("123", utility("fissa", "monoraria", 0.145, 72.0), gasPtr(utility("fissa", "monoraria", 0.456, 84.0)))
	// Nuove tariffe entrambe migliorano
	current := merge(
		tariff("luce", "fissa", "monoraria", rate(0.130, 65.0)),
		tariff("gas", "fissa", "monoraria", rate(0.420, 80.0)),
	)

	texts, _ := runCheck(t, user, current)

	messageText := singleText(t, texts)
	// Verifica che il messaggio contenga ENTRAMBE le sezioni
	assertContains(t, messageText, "💡")
	assertContains(t, messageText, "Luce")
	assertContains(t, messageText, "🔥")
	assertContains(t, messageText, "Gas")
}

// test_check_and_notify_both_utilities_mixed_with_savings
func TestCheckAndNotifyBothUtilitiesMixedWithSavings(t *testing.T) {
	luce := utility("fissa", "monoraria", 0.145, 72.0)
	luce.ConsumoF1 = fp(2700.0)
	gas := utility("fissa", "monoraria", 0.456, 84.0)
	gas.ConsumoAnnuo = fp(1200.0)
	// Luce: risparmio 27.5 €/anno; gas: risparmio 39.2 €/anno
	current := merge(
		tariff("luce", "fissa", "monoraria", rate(0.130, 85.0)),
		tariff("gas", "fissa", "monoraria", rate(0.420, 88.0)),
	)

	texts, _ := runCheck(t, userWith("123", luce, &gas), current)

	messageText := singleText(t, texts)
	// Verifica che il messaggio contenga ENTRAMBE le stime
	for _, s := range []string{"💡", "Luce", "🔥", "Gas",
		"💰 In base ai tuoi consumi di luce", "27,50 €/anno",
		"💰 In base ai tuoi consumi di gas", "39,20 €/anno"} {
		assertContains(t, messageText, s)
	}
}

// test_check_and_notify_both_utilities_mixed_without_consumption
func TestCheckAndNotifyBothUtilitiesMixedWithoutConsumption(t *testing.T) {
	user := userWith("123", utility("fissa", "monoraria", 0.145, 72.0), gasPtr(utility("fissa", "monoraria", 0.456, 84.0)))
	// Nuove tariffe entrambe MIXED
	current := merge(
		tariff("luce", "fissa", "monoraria", rate(0.130, 85.0)),
		tariff("gas", "fissa", "monoraria", rate(0.420, 88.0)),
	)

	texts, _ := runCheck(t, user, current)

	messageText := singleText(t, texts)
	// Verifica che il messaggio contenga ENTRAMBE le sezioni e suggerimento
	for _, s := range []string{"💡", "Luce", "🔥", "Gas",
		"📊 In questi casi la convenienza dipende dai tuoi consumi", "/update"} {
		assertContains(t, messageText, s)
	}
}

// test_check_and_notify_luce_non_mixed_gas_mixed_positive
func TestCheckAndNotifyLuceNonMixedGasMixedPositive(t *testing.T) {
	gas := utility("fissa", "monoraria", 0.456, 84.0)
	gas.ConsumoAnnuo = fp(1200.0)
	user := userWith("123", utility("fissa", "monoraria", 0.145, 72.0), &gas)
	// Luce non-MIXED (tutto migliora), Gas MIXED con risparmio positivo
	current := merge(
		tariff("luce", "fissa", "monoraria", rate(0.130, 65.0)),
		tariff("gas", "fissa", "monoraria", rate(0.420, 88.0)),
	)

	texts, _ := runCheck(t, user, current)

	messageText := singleText(t, texts)
	for _, s := range []string{"💡", "Luce", "🔥", "Gas"} {
		assertContains(t, messageText, s)
	}
	// Gas dovrebbe avere la stima
	assertContains(t, messageText, "💰 In base ai tuoi consumi di gas")
}

// test_check_and_notify_luce_mixed_negative_gas_non_mixed
func TestCheckAndNotifyLuceMixedNegativeGasNonMixed(t *testing.T) {
	luce := utility("fissa", "monoraria", 0.145, 72.0)
	luce.ConsumoF1 = fp(2700.0)
	user := userWith("123", luce, gasPtr(utility("fissa", "monoraria", 0.456, 84.0)))
	// Luce MIXED con risparmio negativo, Gas non-MIXED conveniente
	current := merge(
		tariff("luce", "fissa", "monoraria", rate(0.140, 95.0)),
		tariff("gas", "fissa", "monoraria", rate(0.420, 80.0)),
	)

	texts, _ := runCheck(t, user, current)

	messageText := singleText(t, texts)
	// Verifica che non ci sia la sezione luce (cerca sia emoji che parola)
	if strings.Contains(messageText, "💡") && strings.Contains(messageText, "Luce") {
		t.Errorf("la sezione luce non doveva comparire: %q", messageText)
	}
	assertContains(t, messageText, "🔥")
	assertContains(t, messageText, "Gas")
}

// test_check_and_notify_both_mixed_negative_savings
func TestCheckAndNotifyBothMixedNegativeSavings(t *testing.T) {
	logs := captureLogs(t)
	luce := utility("fissa", "monoraria", 0.130, 65.0)
	luce.ConsumoF1 = fp(2700.0)
	gas := utility("fissa", "monoraria", 0.400, 60.0)
	gas.ConsumoAnnuo = fp(1200.0)
	// Luce: 13.5 - 25 = -11.5 €; gas: 12 - 40 = -28 € → skip con log per entrambi
	current := merge(
		tariff("luce", "fissa", "monoraria", rate(0.125, 90.0)),
		tariff("gas", "fissa", "monoraria", rate(0.390, 100.0)),
	)

	texts, _ := runCheck(t, userWith("123", luce, &gas), current)

	// Verifica che NON sia stata inviata alcuna notifica
	if len(texts) != 0 {
		t.Errorf("nessuna notifica attesa, ottenute %d", len(texts))
	}
	out := logs.String()
	assertContains(t, out, "INFO - ⏭️  Utente 123: luce MIXED stimato -11.50 €/anno, non conveniente")
	assertContains(t, out, "INFO - ⏭️  Utente 123: gas MIXED stimato -28.00 €/anno, non conveniente")
}
