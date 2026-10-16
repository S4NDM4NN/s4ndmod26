package main

// Retail RTCW pak table: size and sha256 of the official 1.41 files.  Keep in sync with RETAIL in
// wasm/shell.html (TestRetailTableMatchesBrowser compares the two).

type retailPak struct {
	Name   string
	Size   int64
	SHA256 string
}

// retailPaks lists every file the launcher recognises; retailRequired are the ones a complete install needs
// (pak0 plus mp_pak0-5, the same set the game's own checksum tables list), the mp_pakmaps packs are optional.
var retailPaks = []retailPak{
	{"pak0.pk3", 315823656, "1dfcfc3187ba09fe32cd8a7921a09cd15dd789816186c89b11e5e0d954c55e6c"},
	{"mp_pak0.pk3", 63211149, "c0e090e7b4245313bea64b5fb20b24d69786e593716cfe6bf85ee9b4f3c794b3"},
	{"mp_pak1.pk3", 4449596, "248ff7c6a4c12f3fc08e0bb73a0602ffdb2e31c4a4c50929d76fa5f814cbf974"},
	{"mp_pak2.pk3", 1118, "0a9142be2539427189cc9b31d5af188298ce3e4c8f06d752e1809d22046b7b03"},
	{"mp_pak3.pk3", 159762, "ad7705f8c05154f6e009f29d1a7e3eb5bf9cb26cb9035d9c55640ec02cb14692"},
	{"mp_pak4.pk3", 7799, "61bf3e778d2777895ce7c0c7afe6df97f2521a3e5ed054c04346ea5ccdcac0aa"},
	{"mp_pak5.pk3", 167075, "8defe8227bd345c485bac98f8c1191c2360d0c784953e9c38a81d245eb1901b9"},
	{"mp_pakmaps0.pk3", 3485134, "2e9cfdc505587dc7e7059e3188b9a1b4ceba75decfa7578c3b2a73dbb0d94a93"},
	{"mp_pakmaps1.pk3", 3765933, "a907dc0caf8ab4284dc8ba8de04a11ec8472a50f861998994c834ff1a4781d9a"},
	{"mp_pakmaps2.pk3", 7408542, "d2cc0a7a762ad5f1ec3523ff79798cd7d732ffe8f29e6ac7df66f74319e85eb6"},
	{"mp_pakmaps3.pk3", 10780146, "524ecd9053d4c8f8e27d4bc4c04504fc1a1f3a29dd8299c8b38909db3c51e6ac"},
	{"mp_pakmaps4.pk3", 24171802, "1357a12e57634dac59cbe70a5984af85a43e7db5aff930d05c8ee42dfcefded8"},
	{"mp_pakmaps5.pk3", 20024689, "85cc0e8358a26601211625a9600ca47c126e4a38cca0bac60d7465ffb5ccadfe"},
	{"mp_pakmaps6.pk3", 10080181, "fcdb3754ca4e9f60b3086c8461b26160dd8de7f5e94c04100a2eb6f9519abca2"},
}

var retailRequired = []string{"pak0.pk3", "mp_pak0.pk3", "mp_pak1.pk3", "mp_pak2.pk3", "mp_pak3.pk3", "mp_pak4.pk3", "mp_pak5.pk3"}
