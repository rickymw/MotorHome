package iracing

// Player-car scalars read from shared memory alongside the CarIdx arrays.
//
// These types are platform-neutral (unlike LiveData, which lives in the
// Windows-only read path) so the fuel tracker and the enum decoders that
// consume them compile and test anywhere.
//
// Variable names, units and types here were checked against the table a
// running iRacing build publishes, not only the SDK header: the header is
// several years older than some of these (Precipitation, TrackWetness,
// WeatherDeclaredWet arrived with dynamic weather).

// LapDelta is one of iRacing's rolling delta-time channels, e.g.
// LapDeltaToBestLap with its _DD and _OK companions.
type LapDelta struct {
	Seconds float32 // LapDeltaTo<Ref>: negative is ahead of the reference lap
	Rate    float32 // LapDeltaTo<Ref>_DD, s/s: positive means currently losing time
	// Valid mirrors LapDeltaTo<Ref>_OK. iRacing keeps publishing a number when
	// there is no reference (an out lap, no session best yet) — observed as a
	// stale or zero delta alongside _OK=false — so the value must not be shown
	// without it. A build that does not publish the variable reads as false.
	Valid bool
}

// LapTiming is the player's lap clock and deltas.
//
// Lap times of zero or below mean "none yet": iRacing publishes 0 for
// LapBestLapTime and LapLastLapTime before a lap has been completed, and -1
// for a last lap that was invalidated.
type LapTiming struct {
	Current    float32 // LapCurrentLapTime — the F3 box estimate
	Last       float32 // LapLastLapTime
	Best       float32 // LapBestLapTime
	BestLapNum int32   // LapBestLap

	ToBest           LapDelta // LapDeltaToBestLap — player's best this session
	ToOptimal        LapDelta // LapDeltaToOptimalLap — player's best sectors combined
	ToSessionBest    LapDelta // LapDeltaToSessionBestLap — fastest lap by anyone
	ToSessionOptimal LapDelta // LapDeltaToSessionOptimalLap
	ToLast           LapDelta // LapDeltaToSessionLastlLap (sic — iRacing's spelling)
}

// FuelState is the tank as iRacing reports it at one instant.
type FuelState struct {
	// Available is false when FuelLevel is not published, which is different
	// from an empty tank.
	Available bool
	Litres    float32 // FuelLevel, l
	Pct       float32 // FuelLevelPct, 0–1
	// UsePerHourKg is FuelUsePerHour: instantaneous, and in kg/h rather than
	// litres because iRacing does not publish the fuel's density. Nothing is
	// derived from it — see FuelTracker for why consumption comes from level
	// differences instead.
	UsePerHourKg float32
}

// Conditions is the weather at the start/finish line. Every field is a pointer
// because each is an independent variable that a given build may not publish;
// nil means "not available", which must not be rendered as zero degrees or a
// dry track.
type Conditions struct {
	AirTempC      *float32 // AirTemp
	TrackTempC    *float32 // TrackTempCrew (TrackTemp is deprecated and mirrors it)
	Humidity      *float32 // RelativeHumidity, 0–1
	WindMS        *float32 // WindVel, m/s
	WindDirRad    *float32 // WindDir, radians
	AirPressurePa *float32 // AirPressure, Pa
	AirDensity    *float32 // AirDensity, kg/m³
	FogLevel      *float32 // FogLevel, 0–1
	Precipitation *float32 // Precipitation, 0–1
	Skies         *int32   // Skies, see SkiesName
	TrackWetness  *int32   // TrackWetness, see TrackWetnessName
	DeclaredWet   *bool    // WeatherDeclaredWet — stewards allow wet tyres
}

// SkiesName decodes the Skies variable (0=clear … 3=overcast, per the
// variable's own description string).
func SkiesName(v int32) string {
	switch v {
	case 0:
		return "Clear"
	case 1:
		return "Partly cloudy"
	case 2:
		return "Mostly cloudy"
	case 3:
		return "Overcast"
	}
	return "Unknown"
}

// TrackWetnessName decodes irsdk_TrackWetness.
func TrackWetnessName(v int32) string {
	switch v {
	case 1:
		return "Dry"
	case 2:
		return "Mostly dry"
	case 3:
		return "Very lightly wet"
	case 4:
		return "Lightly wet"
	case 5:
		return "Moderately wet"
	case 6:
		return "Very wet"
	case 7:
		return "Extremely wet"
	}
	return "Unknown"
}
