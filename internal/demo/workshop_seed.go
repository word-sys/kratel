package demo

import (
	"context"
	"fmt"
	"time"

	"github.com/word-sys/kratel/internal/catalog"
	"github.com/word-sys/kratel/internal/db"
	"github.com/word-sys/kratel/internal/ledger"
	"github.com/word-sys/kratel/internal/models"
)

// SeedResult reports the entities created by SeedWorkshopDemo.
type SeedResult struct {
	AlreadySeeded       bool `json:"already_seeded"`
	LocationsCreated    int  `json:"locations_created"`
	SuppliersCreated    int  `json:"suppliers_created"`
	ItemsCreated        int  `json:"items_created"`
	AliasesCreated      int  `json:"aliases_created"`
	KitsCreated         int  `json:"kits_created"`
	LoansCreated        int  `json:"loans_created"`
	AuditsCreated       int  `json:"audits_created"`
	TransactionsCreated int  `json:"transactions_created"`
}

// SeedWorkshopDemo populates a realistic electronics & repair workshop inventory dataset
// (hierarchical bins, 3 suppliers, 18 items in pcs/m/kg/L/roll/syringe, multi-barcode aliases,
// custom JSON attributes, BOM kits, active tool loans, an open cycle count, and immutable ledger history).
// If the catalog already contains items and force is false, it returns AlreadySeeded=true without changes.
func SeedWorkshopDemo(ctx context.Context, store *db.Store, force bool) (*SeedResult, error) {
	var existingCount int
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM items;`).Scan(&existingCount); err != nil {
		return nil, fmt.Errorf("check existing items: %w", err)
	}
	if existingCount > 0 && !force {
		return &SeedResult{AlreadySeeded: true}, nil
	}

	cat := catalog.NewService(store)
	led := ledger.NewService(store, cat)
	res := &SeedResult{}

	// 1. Hierarchical Storage Locations
	locMap := make(map[string]int64)
	createLoc := func(code, name string, parentCode, notes string) (int64, error) {
		if existing, err := cat.GetLocationByCode(ctx, code); err == nil {
			locMap[code] = existing.ID
			return existing.ID, nil
		}
		var parentID *int64
		if parentCode != "" {
			if pid, ok := locMap[parentCode]; ok {
				parentID = &pid
			}
		}
		loc, err := cat.CreateLocation(ctx, catalog.LocationInput{
			Code:     code,
			Name:     name,
			ParentID: parentID,
			Notes:    notes,
		})
		if err != nil {
			return 0, err
		}
		locMap[code] = loc.ID
		res.LocationsCreated++
		return loc.ID, nil
	}

	locSpecs := []struct {
		code, name, parent, notes string
	}{
		{"WS-MAIN", "Electronics & Repair Workshop", "", "Primary workshop floor"},
		{"RACK-A", "Rack A — Passives & Semis", "WS-MAIN", "ESD-safe component racks"},
		{"A-01-B1", "SMD Reels & Passives (0805/0603)", "RACK-A", "Resistors, ceramic capacitors, diodes"},
		{"A-02-B2", "MCUs, Modules & Displays", "RACK-A", "Anti-static foam trays"},
		{"A-03-B3", "Power & Connectors", "RACK-A", "LDOs, buck converters, USB-C headers"},
		{"RACK-B", "Rack B — Wire & Consumables", "WS-MAIN", "Cables, solder, fluxes, tapes"},
		{"B-01-W1", "Silicone & Hookup Wire Spools", "RACK-B", "Measured in meters"},
		{"B-02-C1", "Solder, Paste & Chemicals", "RACK-B", "Cool shelf for pastes & solvents"},
		{"RACK-C", "Rack C — Fasteners & Hardware", "WS-MAIN", "Metric screws, standoffs, lock nuts"},
		{"C-01-M3", "M3 Stainless & Brass Bin", "RACK-C", "DIN 912 / DIN 985 drawers"},
		{"LAB-GEAR", "Test Bench & Loaner Equipment", "WS-MAIN", "Returnable meters & logic analyzers"},
	}
	for _, ls := range locSpecs {
		if _, err := createLoc(ls.code, ls.name, ls.parent, ls.notes); err != nil {
			return nil, fmt.Errorf("seed location %s: %w", ls.code, err)
		}
	}

	// 2. Suppliers
	supMap := make(map[string]int64)
	supSpecs := []catalog.SupplierInput{
		{Code: "SUP-MOUSER", Name: "Mouser / DigiKey Express", Contact: "Order Desk", Email: "orders@mouser.example", Phone: "+1-800-346-6873", Notes: "Semiconductors, MCUs, SMD reels"},
		{Code: "SUP-ANADOLU", Name: "Anadolu Elektronik & Sarf", Contact: "Mehmet Demir", Email: "satis@anadolu-sarf.example", Phone: "+90-212-555-0142", Notes: "Local soldering, wire & bench supplies"},
		{Code: "SUP-NORDIC", Name: "Nordic Metric & Hardware", Contact: "Soren Lindqvist", Email: "supply@nordicmetric.example", Phone: "+46-8-555-0190", Notes: "A2 stainless metric fasteners & standoffs"},
	}
	for _, ss := range supSpecs {
		sup, err := cat.CreateSupplier(ctx, ss)
		if err != nil {
			return nil, fmt.Errorf("seed supplier %s: %w", ss.Code, err)
		}
		supMap[ss.Code] = sup.ID
		res.SuppliersCreated++
	}

	now := time.Now().UTC()
	soonExpiry := now.AddDate(0, 0, 14).Format("2006-01-02")
	expiredDate := now.AddDate(0, 0, -5).Format("2006-01-02")
	okExpiry := now.AddDate(1, 3, 0).Format("2006-01-02")

	// 3. 18 Realistic Workshop Items across units & categories
	itemMap := make(map[string]int64)
	type seedItemSpec struct {
		in      models.ItemInput
		locCode string
		supCode string
		aliases []struct {
			code  string
			atype models.AliasType
			notes string
		}
	}

	items := []seedItemSpec{
		{
			locCode: "A-02-B2", supCode: "SUP-MOUSER",
			in: models.ItemInput{
				SKU: "MCU-ESP32-S3", Barcode: "869000100101", Name: "ESP32-S3-DevKitC-1 N16R8",
				Description: "Dual-core Wi-Fi + BLE 5 MCU board, 16MB Flash, 8MB PSRAM",
				Category: "Microcontrollers", Unit: "pcs", MinStock: "6", UnitCost: "7.80",
				Attributes: map[string]string{"voltage": "3.3V", "flash": "16MB", "interface": "USB-C"},
				OpeningQuantity: "14", Operator: "Karim",
			},
			aliases: []struct {
				code  string
				atype models.AliasType
				notes string
			}{
				{"MOUSER-ESP32S3", models.AliasSupplierPart, "Mouser reel sticker"},
				{"ESP32-S3-DEV", models.AliasLegacySKU, "Legacy spreadsheet SKU"},
			},
		},
		{
			locCode: "A-02-B2", supCode: "SUP-MOUSER",
			in: models.ItemInput{
				SKU: "MCU-RP2040-PICO", Barcode: "869000100102", Name: "Raspberry Pi Pico H",
				Description: "RP2040 Dual Cortex-M0+ board with pre-soldered headers",
				Category: "Microcontrollers", Unit: "pcs", MinStock: "8", UnitCost: "5.20",
				Attributes: map[string]string{"voltage": "3.3V", "flash": "2MB", "interface": "Micro-USB"},
				OpeningQuantity: "4", Operator: "Karim", // Intentionally low stock
			},
		},
		{
			locCode: "A-01-B1", supCode: "SUP-MOUSER",
			in: models.ItemInput{
				SKU: "RES-10K-0805", Barcode: "869000100103", Name: "10kΩ 1% SMD Resistor (0805)",
				Description: "Thick film chip resistor 1/8W ±1% cut tape",
				Category: "Passives", Unit: "pcs", MinStock: "250", UnitCost: "0.005",
				Attributes: map[string]string{"package": "0805", "tolerance": "1%", "power": "0.125W"},
				OpeningQuantity: "1000", Operator: "Karim",
			},
			aliases: []struct {
				code  string
				atype models.AliasType
				notes string
			}{
				{"LCSC-C17414", models.AliasSupplierPart, "LCSC cut-tape code"},
			},
		},
		{
			locCode: "A-01-B1", supCode: "SUP-MOUSER",
			in: models.ItemInput{
				SKU: "CAP-100N-0805", Barcode: "869000100104", Name: "100nF 50V X7R Ceramic Cap (0805)",
				Description: "Decoupling MLCC capacitor 100nF 50V",
				Category: "Passives", Unit: "pcs", MinStock: "300", UnitCost: "0.008",
				Attributes: map[string]string{"package": "0805", "voltage": "50V", "dielectric": "X7R"},
				OpeningQuantity: "850", Operator: "Karim",
			},
		},
		{
			locCode: "A-03-B3", supCode: "SUP-MOUSER",
			in: models.ItemInput{
				SKU: "REG-AMS1117-33", Barcode: "869000100105", Name: "AMS1117-3.3V LDO Regulator (SOT-223)",
				Description: "1A fixed 3.3V linear voltage regulator",
				Category: "Power & Regulators", Unit: "pcs", MinStock: "25", UnitCost: "0.22",
				Attributes: map[string]string{"package": "SOT-223", "voltage": "3.3V", "current": "1A"},
				OpeningQuantity: "18", Operator: "Karim", // Low stock
			},
			aliases: []struct {
				code  string
				atype models.AliasType
				notes string
			}{
				{"DIGIKEY-AMS1117", models.AliasSupplierPart, "DigiKey bag barcode"},
			},
		},
		{
			locCode: "A-03-B3", supCode: "SUP-MOUSER",
			in: models.ItemInput{
				SKU: "CONN-USBC-16P", Barcode: "869000100106", Name: "USB Type-C 16-Pin SMD Receptacle",
				Description: "Mid-mount USB-C 2.0 repair replacement socket",
				Category: "Connectors", Unit: "pcs", MinStock: "20", UnitCost: "0.45",
				Attributes: map[string]string{"pins": "16P", "mounting": "SMD", "rating": "5A"},
				OpeningQuantity: "65", Operator: "Karim",
			},
		},
		{
			locCode: "A-02-B2", supCode: "SUP-ANADOLU",
			in: models.ItemInput{
				SKU: "DISP-OLED-096", Barcode: "869000100107", Name: "0.96\" I2C OLED Display (SSD1306)",
				Description: "128x64 monochrome white I2C OLED module",
				Category: "Displays", Unit: "pcs", MinStock: "10", UnitCost: "3.10",
				Attributes: map[string]string{"voltage": "3.3V-5V", "interface": "I2C", "resolution": "128x64"},
				OpeningQuantity: "5", Operator: "Barın", // Low stock
			},
		},
		{
			locCode: "B-01-W1", supCode: "SUP-ANADOLU",
			in: models.ItemInput{
				SKU: "WIRE-18AWG-RED", Barcode: "869000100108", Name: "Silicone Hookup Wire 18AWG Red",
				Description: "Ultra-flexible 200C tinned copper silicone wire",
				Category: "Cables & Wire", Unit: "m", MinStock: "15", UnitCost: "1.35",
				Attributes: map[string]string{"gauge": "18AWG", "color": "Red", "temp_rating": "200C"},
				OpeningQuantity: "24.5", Operator: "Karim",
			},
		},
		{
			locCode: "B-01-W1", supCode: "SUP-ANADOLU",
			in: models.ItemInput{
				SKU: "WIRE-18AWG-BLK", Barcode: "869000100109", Name: "Silicone Hookup Wire 18AWG Black",
				Description: "Ultra-flexible 200C tinned copper silicone wire",
				Category: "Cables & Wire", Unit: "m", MinStock: "15", UnitCost: "1.35",
				Attributes: map[string]string{"gauge": "18AWG", "color": "Black", "temp_rating": "200C"},
				OpeningQuantity: "6.25", Operator: "Karim", // Low stock
			},
		},
		{
			locCode: "B-02-C1", supCode: "SUP-ANADOLU",
			in: models.ItemInput{
				SKU: "SOLDER-SAC305", Barcode: "869000100110", Name: "SAC305 Lead-Free Solder Wire 0.8mm",
				Description: "Sn96.5 Ag3.0 Cu0.5 no-clean rosin core solder",
				Category: "Soldering", Unit: "kg", MinStock: "0.75", UnitCost: "42.00",
				LotNumber: "LOT-SAC-2604", ExpiryDate: okExpiry,
				Attributes: map[string]string{"alloy": "SAC305", "diameter": "0.8mm"},
				OpeningQuantity: "1.75", Operator: "Barın",
			},
		},
		{
			locCode: "B-02-C1", supCode: "SUP-ANADOLU",
			in: models.ItemInput{
				SKU: "PASTE-SN42-BI58", Barcode: "869000100111", Name: "Low-Temp Sn42/Bi58 Solder Paste (50g)",
				Description: "138C melting point SMD rework paste syringe",
				Category: "Soldering", Unit: "syringe", MinStock: "2", UnitCost: "14.50",
				LotNumber: "LOT-PST-09", ExpiryDate: soonExpiry,
				Attributes: map[string]string{"alloy": "Sn42Bi58", "melt_temp": "138C"},
				OpeningQuantity: "4", Operator: "Barın",
			},
		},
		{
			locCode: "B-02-C1", supCode: "SUP-ANADOLU",
			in: models.ItemInput{
				SKU: "CHEM-IPA-999", Barcode: "869000100112", Name: "Isopropyl Alcohol 99.9% PCB Cleaner",
				Description: "Electronics-grade anhydrous IPA flux remover",
				Category: "Chemicals", Unit: "L", MinStock: "2", UnitCost: "6.80",
				LotNumber: "LOT-IPA-44", ExpiryDate: okExpiry,
				Attributes: map[string]string{"purity": "99.9%", "flammable": "Yes"},
				OpeningQuantity: "4.5", Operator: "Karim",
			},
		},
		{
			locCode: "B-02-C1", supCode: "SUP-ANADOLU",
			in: models.ItemInput{
				SKU: "TAPE-KAPTON-20", Barcode: "869000100113", Name: "Polyimide Kapton Tape (20mm x 33m)",
				Description: "High-temp heat shielding tape for hot-air rework",
				Category: "Soldering", Unit: "roll", MinStock: "3", UnitCost: "4.90",
				Attributes: map[string]string{"width": "20mm", "temp_rating": "260C"},
				OpeningQuantity: "7", Operator: "Barın",
			},
		},
		{
			locCode: "B-02-C1", supCode: "SUP-ANADOLU",
			in: models.ItemInput{
				SKU: "THERM-MX4-4G", Barcode: "869000100114", Name: "High-Conductivity Thermal Paste (4g)",
				Description: "Non-conductive CPU/GPU & heatsink thermal compound",
				Category: "Chemicals", Unit: "syringe", MinStock: "3", UnitCost: "7.20",
				LotNumber: "LOT-THM-22", ExpiryDate: expiredDate,
				Attributes: map[string]string{"conductivity": "8.5 W/mK", "weight": "4g"},
				OpeningQuantity: "2", Operator: "Elena", // Both low stock & expired
			},
		},
		{
			locCode: "C-01-M3", supCode: "SUP-NORDIC",
			in: models.ItemInput{
				SKU: "FAST-M3X8-HEX", Barcode: "869000100115", Name: "M3x8 Stainless A2 Hex Socket Bolt",
				Description: "DIN 912 M3x8mm stainless steel machine screw",
				Category: "Fasteners", Unit: "pcs", MinStock: "100", UnitCost: "0.03",
				Attributes: map[string]string{"thread": "M3", "length": "8mm", "material": "A2-304"},
				OpeningQuantity: "420", Operator: "Karim",
			},
			aliases: []struct {
				code  string
				atype models.AliasType
				notes string
			}{
				{"DIN912-M3-8", models.AliasSupplierPart, "Nordic box code"},
			},
		},
		{
			locCode: "C-01-M3", supCode: "SUP-NORDIC",
			in: models.ItemInput{
				SKU: "FAST-M3-NUT", Barcode: "869000100116", Name: "M3 Nylon Lock Nut (DIN 985)",
				Description: "Stainless A2 M3 vibration-resistant lock nut",
				Category: "Fasteners", Unit: "pcs", MinStock: "100", UnitCost: "0.025",
				Attributes: map[string]string{"thread": "M3", "material": "A2-304"},
				OpeningQuantity: "360", Operator: "Karim",
			},
		},
		{
			locCode: "LAB-GEAR", supCode: "SUP-ANADOLU",
			in: models.ItemInput{
				SKU: "TOOL-FLUKE-87V", Barcode: "869000100117", Name: "Fluke 87V Industrial Multimeter",
				Description: "True-RMS digital multimeter with temperature probe",
				Category: "Test Equipment", Unit: "pcs", MinStock: "1", UnitCost: "390.00",
				Attributes: map[string]string{"cat_rating": "CAT IV 600V", "calibration": "Annual"},
				OpeningQuantity: "3", Operator: "Barın",
			},
		},
		{
			locCode: "LAB-GEAR", supCode: "SUP-MOUSER",
			in: models.ItemInput{
				SKU: "TOOL-SALEAE-8", Barcode: "869000100118", Name: "8-Channel USB Logic Analyzer",
				Description: "24MHz/100MHz SPI/I2C/UART protocol analyzer + test clips",
				Category: "Test Equipment", Unit: "pcs", MinStock: "1", UnitCost: "145.00",
				Attributes: map[string]string{"channels": "8", "interface": "USB-C"},
				OpeningQuantity: "2", Operator: "Barın",
			},
		},
	}

	for _, spec := range items {
		locID := locMap[spec.locCode]
		supID := supMap[spec.supCode]
		spec.in.DefaultLocationID = &locID
		spec.in.OpeningLocationID = &locID
		spec.in.SupplierID = &supID

		created, err := cat.CreateItem(ctx, spec.in)
		if err != nil {
			return nil, fmt.Errorf("seed item %s: %w", spec.in.SKU, err)
		}
		itemMap[created.SKU] = created.ID
		res.ItemsCreated++
		res.TransactionsCreated++ // Opening IN tx

		for _, al := range spec.aliases {
			if _, err := cat.AddItemAlias(ctx, created.ID, al.code, al.atype, al.notes); err != nil {
				return nil, fmt.Errorf("seed alias %s: %w", al.code, err)
			}
			res.AliasesCreated++
		}
	}

	// 4. Seed realistic historical IN / OUT / TRANSFER / ADJUST ledger movements
	mainID := locMap["WS-MAIN"]
	a02ID := locMap["A-02-B2"]
	b01ID := locMap["B-01-W1"]
	c01ID := locMap["C-01-M3"]

	movements := []ledger.MovementInput{
		{ItemID: itemMap["MCU-ESP32-S3"], TxType: ledger.TxOut, Quantity: "2", FromLocationID: &a02ID, Reason: "Customer IoT Prototype #412", Operator: "Elena"},
		{ItemID: itemMap["MCU-ESP32-S3"], TxType: ledger.TxTransfer, Quantity: "2", FromLocationID: &a02ID, ToLocationID: &mainID, Reason: "Stage at front repair counter", Operator: "Barın"},
		{ItemID: itemMap["RES-10K-0805"], TxType: ledger.TxOut, Quantity: "150", Reason: "Rev-C PCB batch assembly", Operator: "Elena"},
		{ItemID: itemMap["CONN-USBC-16P"], TxType: ledger.TxOut, Quantity: "6", Reason: "Laptop charging port repairs", Operator: "Barın"},
		{ItemID: itemMap["WIRE-18AWG-RED"], TxType: ledger.TxOut, Quantity: "3.25", FromLocationID: &b01ID, Reason: "Bench power harness", Operator: "Elena"},
		{ItemID: itemMap["SOLDER-SAC305"], TxType: ledger.TxOut, Quantity: "0.25", Reason: "Weekly bench consumption", Operator: "Barın"},
		{ItemID: itemMap["CHEM-IPA-999"], TxType: ledger.TxOut, Quantity: "0.5", Reason: "Ultrasonic bath refill", Operator: "Karim"},
		{ItemID: itemMap["FAST-M3X8-HEX"], TxType: ledger.TxAdjust, AdjustMode: ledger.AdjustModeTarget, Quantity: "400", ToLocationID: &c01ID, Reason: "Spot count tray C-01", Operator: "Karim"},
		{ItemID: itemMap["DISP-OLED-096"], TxType: ledger.TxOut, Quantity: "1", Reason: "Bench tester screen replacement", Operator: "Barın"},
	}
	for _, m := range movements {
		if _, err := led.RecordMovement(ctx, m); err != nil {
			return nil, fmt.Errorf("seed movement for item %d: %w", m.ItemID, err)
		}
		res.TransactionsCreated++
	}

	// 5. Seed 2 BOM / Repair Kits
	if _, err := led.CreateKit(ctx, ledger.KitInput{
		Code:        "IOT-NODE-V2",
		Name:        "ESP32-S3 Sensor Node BOM",
		Description: "Standard workshop build bundle: 1x ESP32-S3, 4x 10kΩ, 4x 100nF, 1x OLED, 4x M3 Bolts",
		Category:    "Assemblies",
		Components: []ledger.KitComponentInput{
			{ItemID: itemMap["MCU-ESP32-S3"], Quantity: "1"},
			{ItemID: itemMap["RES-10K-0805"], Quantity: "4"},
			{ItemID: itemMap["CAP-100N-0805"], Quantity: "4"},
			{ItemID: itemMap["DISP-OLED-096"], Quantity: "1"},
			{ItemID: itemMap["FAST-M3X8-HEX"], Quantity: "4"},
		},
	}); err != nil {
		return nil, fmt.Errorf("seed kit 1: %w", err)
	}
	res.KitsCreated++

	if _, err := led.CreateKit(ctx, ledger.KitInput{
		Code:        "USBC-REWORK",
		Name:        "USB-C Port Repair Kit",
		Description: "1x USB-C 16P socket, 0.1m red wire, 0.1m black wire, 1x LDO regulator",
		Category:    "Repairs",
		Components: []ledger.KitComponentInput{
			{ItemID: itemMap["CONN-USBC-16P"], Quantity: "1"},
			{ItemID: itemMap["WIRE-18AWG-RED"], Quantity: "0.1"},
			{ItemID: itemMap["WIRE-18AWG-BLK"], Quantity: "0.1"},
			{ItemID: itemMap["REG-AMS1117-33"], Quantity: "1"},
		},
	}); err != nil {
		return nil, fmt.Errorf("seed kit 2: %w", err)
	}
	res.KitsCreated++

	// 6. Seed 2 Active Tool Check-Out Loans (1 due soon, 1 overdue)
	if _, err := led.CheckOutLoan(ctx, ledger.LoanCheckOutInput{
		ItemID:   itemMap["TOOL-FLUKE-87V"],
		Quantity: "1",
		Borrower: "Bench 2 — Elena",
		DueAt:    now.AddDate(0, 0, 3).Format("2006-01-02"),
		Operator: "Barın",
		Notes:    "Testing inverter board ripple",
	}); err != nil {
		return nil, fmt.Errorf("seed loan 1: %w", err)
	}
	res.LoansCreated++
	res.TransactionsCreated++

	if _, err := led.CheckOutLoan(ctx, ledger.LoanCheckOutInput{
		ItemID:   itemMap["TOOL-SALEAE-8"],
		Quantity: "1",
		Borrower: "Field Van 1 — Karim",
		DueAt:    now.AddDate(0, 0, -2).Format("2006-01-02"),
		Operator: "Barın",
		Notes:    "On-site Modbus RS-485 capture",
	}); err != nil {
		return nil, fmt.Errorf("seed loan 2: %w", err)
	}
	res.LoansCreated++
	res.TransactionsCreated++

	// 7. Seed 1 Open Physical Cycle-Count Session on Rack A-02-B2
	sess, err := led.CreateAuditSession(ctx, ledger.AuditSessionInput{
		Name:       "Weekly Bin A-02-B2 Spot Check",
		LocationID: &a02ID,
		Operator:   "Barın",
		Notes:      "Verify MCU & OLED tray counts before weekly reorder",
	})
	if err != nil {
		return nil, fmt.Errorf("seed audit session: %w", err)
	}
	if _, err := led.RecordAuditCount(ctx, sess.ID, ledger.AuditCountInput{
		ItemID:        itemMap["MCU-ESP32-S3"],
		LocationID:    &a02ID,
		Quantity:      "10",
		IncrementMode: false,
		Notes:         "Verified anti-static tray",
	}); err != nil {
		return nil, fmt.Errorf("seed audit count: %w", err)
	}
	res.AuditsCreated++

	return res, nil
}
