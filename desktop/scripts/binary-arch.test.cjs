const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');

const { archFromBuffer, binaryArch } = require('./binary-arch.cjs');

function elfHeader(machine) {
  const buf = Buffer.alloc(64);
  buf[0] = 0x7f;
  buf.write('ELF', 1, 'latin1');
  buf.writeUInt16LE(machine, 18);
  return buf;
}

function thinMachO(cputype, littleEndian) {
  const buf = Buffer.alloc(32);
  if (littleEndian) {
    buf.writeUInt32BE(0xcffaedfe, 0);
    buf.writeUInt32LE(cputype, 4);
  } else {
    buf.writeUInt32BE(0xfeedfacf, 0);
    buf.writeUInt32BE(cputype, 4);
  }
  return buf;
}

function fatMachO(cputype, littleEndian) {
  const buf = Buffer.alloc(64);
  if (littleEndian) {
    buf.writeUInt32BE(0xbebafeca, 0);
    buf.writeUInt32LE(cputype, 8);
  } else {
    buf.writeUInt32BE(0xcafebabe, 0);
    buf.writeUInt32BE(cputype, 8);
  }
  return buf;
}

function peHeader(machine) {
  const buf = Buffer.alloc(256);
  buf.write('MZ', 0, 'latin1');
  buf.writeUInt32LE(0x80, 0x3c);
  buf.write('PE\0\0', 0x80, 'latin1');
  buf.writeUInt16LE(machine, 0x84);
  return buf;
}

const X86_64 = 0x01000007;
const ARM64 = 0x0100000c;

test('detects ELF architectures', () => {
  assert.equal(archFromBuffer(elfHeader(62)), 'amd64');
  assert.equal(archFromBuffer(elfHeader(183)), 'arm64');
  assert.equal(archFromBuffer(elfHeader(40)), null);
});

test('detects little-endian thin Mach-O images (the macOS packaged case)', () => {
  assert.equal(archFromBuffer(thinMachO(X86_64, true)), 'amd64');
  assert.equal(archFromBuffer(thinMachO(ARM64, true)), 'arm64');
});

test('detects big-endian thin Mach-O images', () => {
  assert.equal(archFromBuffer(thinMachO(X86_64, false)), 'amd64');
  assert.equal(archFromBuffer(thinMachO(ARM64, false)), 'arm64');
});

test('detects fat Mach-O images from the first slice', () => {
  assert.equal(archFromBuffer(fatMachO(ARM64, false)), 'arm64');
  assert.equal(archFromBuffer(fatMachO(X86_64, true)), 'amd64');
});

test('detects PE architectures', () => {
  assert.equal(archFromBuffer(peHeader(0x8664)), 'amd64');
  assert.equal(archFromBuffer(peHeader(0xaa64)), 'arm64');
  assert.equal(archFromBuffer(peHeader(0x14c)), null);
});

test('returns null for unknown headers', () => {
  assert.equal(archFromBuffer(Buffer.alloc(32)), null);
  assert.equal(archFromBuffer(Buffer.from('short')), null);
});

test('binaryArch reads the header from disk', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'mothx-arch-'));
  try {
    const file = path.join(dir, 'mothx');
    fs.writeFileSync(file, thinMachO(ARM64, true));
    assert.equal(binaryArch(file), 'arm64');
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});
