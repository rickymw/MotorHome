// SettingChangeVisibility.js — SimHub Computed Property
// For each tracked setting, exposes two booleans and a color string:
//   {key}IncreasedVisible / {key}DecreasedVisible — bind to Visibility triggers
//   {key}ChangeColor — bind to color properties directly; no NCalc formula needed

const TICK_PROP = 'DataCorePlugin.CurrentDateTime';

// --- Configurable: add/remove rows to track more or fewer settings ---
const TRACKED = [
    {
        key:       'BrakeBias',
        source:    'DataCorePlugin.GameData.BrakeBias',
        incProp:   'ComputedPropertiesPlugin.BrakeBiasIncreasedVisible',
        decProp:   'ComputedPropertiesPlugin.BrakeBiasDecreasedVisible',
        colorProp: 'ComputedPropertiesPlugin.BrakeBiasChangeColor',
        colorInc:  'YellowGreen',
        colorDec:  'Tomato',
        colorOff:  'Black',
        holdMsInc: 3000,
        holdMsDec: 3000,
        epsilon:   0.005,
    },
    {
        key:       'ABS',
        source:    'DataCorePlugin.GameData.ABSLevel',
        incProp:   'ComputedPropertiesPlugin.ABSIncreasedVisible',
        decProp:   'ComputedPropertiesPlugin.ABSDecreasedVisible',
        colorProp: 'ComputedPropertiesPlugin.ABSChangeColor',
        colorInc:  'YellowGreen',
        colorDec:  'Tomato',
        colorOff:  'Black',
        holdMsInc: 3000,
        holdMsDec: 3000,
        epsilon:   0.5,
    },
];
// ---------------------------------------------------------------------

var previous     = [];
var incUntilMs   = [];
var decUntilMs   = [];
var incIsVisible = [];
var decIsVisible = [];

function init() {
    for (var i = 0; i < TRACKED.length; i++) {
        var t = TRACKED[i];
        createProperty(t.incProp);
        createProperty(t.decProp);
        createProperty(t.colorProp);
        setPropertyValue(t.incProp, false);
        setPropertyValue(t.decProp, false);
        setPropertyValue(t.colorProp, t.colorOff);
        previous.push(null);
        incUntilMs.push(0);
        decUntilMs.push(0);
        incIsVisible.push(false);
        decIsVisible.push(false);
    }

    // Static dispatch: `subscribe` takes a function-name string, not a closure.
    // To track another setting: append to TRACKED, add an onChangeN below, add a subscribe line here.
    subscribe(TRACKED[0].source, 'onChange0');
    subscribe(TRACKED[1].source, 'onChange1');

    subscribe(TICK_PROP, 'onTick');
}

function onChange0() { processChange(0); }
function onChange1() { processChange(1); }

function processChange(i) {
    var t = TRACKED[i];
    var current = getPropertyValue(t.source);
    if (current === null) return;

    var prev = previous[i];
    if (prev !== null) {
        if (current > prev + t.epsilon) {
            decUntilMs[i] = 0;
            setVisible(i, false, false);
            incUntilMs[i] = Date.now() + t.holdMsInc;
            setVisible(i, true, true);
        } else if (current < prev - t.epsilon) {
            incUntilMs[i] = 0;
            setVisible(i, true, false);
            decUntilMs[i] = Date.now() + t.holdMsDec;
            setVisible(i, false, true);
        }
    }
    previous[i] = current;
}

function onTick() {
    var now = Date.now();
    for (var i = 0; i < TRACKED.length; i++) {
        if (incIsVisible[i] && now >= incUntilMs[i]) setVisible(i, true,  false);
        if (decIsVisible[i] && now >= decUntilMs[i]) setVisible(i, false, false);
    }
}

function setVisible(i, incSide, value) {
    var t = TRACKED[i];
    if (incSide) {
        if (value === incIsVisible[i]) return;
        incIsVisible[i] = value;
        setPropertyValue(t.incProp, value);
    } else {
        if (value === decIsVisible[i]) return;
        decIsVisible[i] = value;
        setPropertyValue(t.decProp, value);
    }
    var color = incIsVisible[i] ? t.colorInc : (decIsVisible[i] ? t.colorDec : t.colorOff);
    setPropertyValue(t.colorProp, color);
}
