const fs = require('node:fs');

// Read the CPU architecture of a bare Go binary from its file header so the
// packaging step can refuse to bundle a CLI whose arch does not match the
// Electron app arch. Returns 'amd64', 'arm64', or null if the binary is not a
// recognized ELF / Mach-O / PE image.
//
// Mach-O facts worth remembering (this check silently passed an arm64 binary
// into the x64 macOS package once already):
//   - x86_64/arm64 images are little-endian and start with the bytes
//     cf fa ed fe (MH_MAGIC_64 stored LE); fe ed fa cf is the big-endian form.
//   - Universal/fat images start with ca fe ba be / be ba fe ca (32-bit) or
//     ca fe ba bf / bf ba fe ca (64-bit); the first slice's cputype sits at
//     byte offset 8 of the fat_arch record.
const CPUTYPE_X86_64 = 0x01000007;
const CPUTYPE_ARM64 = 0x0100000c;

function machArch(cputype) {
  if (cputype === CPUTYPE_X86_64) return 'amd64';
  if (cputype === CPUTYPE_ARM64) return 'arm64';
  return null;
}

function archFromBuffer(buf) {
  if (buf.length < 8) return null;
  if (buf[0] === 0x7f && buf[1] === 0x45 && buf[2] === 0x4c && buf[3] === 0x46) {
    // ELF: e_machine (2 bytes) at offset 18.
    if (buf.length < 20) return null;
    const machine = buf.readUInt16LE(18);
    if (machine === 62) return 'amd64'; // EM_X86_64
    if (machine === 183) return 'arm64'; // EM_AARCH64
    return null;
  }
  const magic = buf.readUInt32BE(0);
  if (magic === 0xfeedfacf || magic === 0xfeedface) {
    // Thin Mach-O, big-endian header fields.
    return machArch(buf.readUInt32BE(4));
  }
  if (magic === 0xcffaedfe || magic === 0xcefaedfe) {
    // Thin Mach-O, little-endian header fields (x86_64/arm64 images).
    return machArch(buf.readUInt32LE(4));
  }
  if (magic === 0xcafebabe || magic === 0xcafebabf) {
    // Fat Mach-O, big-endian header fields; first slice cputype at offset 8.
    return machArch(buf.readUInt32BE(8));
  }
  if (magic === 0xbebafeca || magic === 0xbfbafeca) {
    // Fat Mach-O, little-endian header fields.
    return machArch(buf.readUInt32LE(8));
  }
  if (buf[0] === 0x4d && buf[1] === 0x5a) {
    // PE: e_lfanew at 0x3c, then PE signature, then COFF Machine (2 bytes).
    if (buf.length < 0x40) return null;
    const peOffset = buf.readUInt32LE(0x3c);
    if (peOffset + 6 > buf.length) return null;
    if (buf[peOffset] !== 0x50 || buf[peOffset + 1] !== 0x45 || buf[peOffset + 2] !== 0 || buf[peOffset + 3] !== 0) return null;
    const machine = buf.readUInt16LE(peOffset + 4);
    if (machine === 0x8664) return 'amd64'; // IMAGE_FILE_MACHINE_AMD64
    if (machine === 0xaa64) return 'arm64'; // IMAGE_FILE_MACHINE_ARM64
    return null;
  }
  return null;
}

function binaryArch(file) {
  const fd = fs.openSync(file, 'r');
  try {
    const buf = Buffer.alloc(256);
    const n = fs.readSync(fd, buf, 0, buf.length, 0);
    return archFromBuffer(buf.subarray(0, n));
  } finally {
    fs.closeSync(fd);
  }
}

module.exports = { binaryArch, archFromBuffer };
