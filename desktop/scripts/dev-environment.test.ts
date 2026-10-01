import assert from 'node:assert/strict';
import test from 'node:test';

import { displayFailureHelp, displayProblem, looksLikeDisplayFailure } from './dev-environment.ts';

test('a Linux host without any display server is reported before the build', () => {
  const problem = displayProblem({ platform: 'linux', env: {} });
  assert.ok(problem, 'a headless Linux host must be reported');
  assert.match(problem, /DISPLAY/);
  assert.match(problem, /WAYLAND_DISPLAY/);
});

test('the help names the white-window symptom and lists the ways out', () => {
  const help = displayFailureHelp();
  assert.match(help, /blank white window/, 'must name the white-window symptom');
  assert.match(help, /ssh -X/, 'must offer X11 forwarding');
  assert.match(help, /Xvfb/, 'must offer a virtual display');
  assert.match(help, /mothx serve/, 'must offer a terminal-friendly alternative');
});

test('X11 and Wayland sessions are accepted', () => {
  assert.equal(displayProblem({ platform: 'linux', env: { DISPLAY: ':0' } }), null);
  assert.equal(displayProblem({ platform: 'linux', env: { DISPLAY: 'localhost:10.0' } }), null);
  assert.equal(displayProblem({ platform: 'linux', env: { WAYLAND_DISPLAY: 'wayland-0' } }), null);
});

test('a display variable that is set but empty still counts as missing', () => {
  assert.ok(displayProblem({ platform: 'linux', env: { DISPLAY: '' } }));
});

test('macOS and Windows have no such requirement', () => {
  assert.equal(displayProblem({ platform: 'darwin', env: {} }), null);
  assert.equal(displayProblem({ platform: 'win32', env: {} }), null);
});

test("Chromium's own display failures are recognised from its output", () => {
  const reported = [
    '[372095:0930/041222.204266:ERROR:ui/ozone/platform/x11/ozone_platform_x11.cc:249] Missing X server or $DISPLAY',
    '[372095:0930/041222.204313:ERROR:ui/aura/env.cc:257] The platform failed to initialize.  Exiting.',
    '(electron:1): Could not open X display',
    'Failed to open X display',
    'The DISPLAY environment variable is not set.',
  ];
  for (const line of reported) {
    assert.equal(looksLikeDisplayFailure(line), true, `should match: ${line}`);
  }
});

test('a display that exists but cannot be connected to is caught by output, not by the env check', () => {
  // A stale or unreachable DISPLAY passes the environment check, so only
  // Chromium's diagnostic can explain the white window.
  const env = { platform: 'linux' as const, env: { DISPLAY: ':0.0' } };
  assert.equal(displayProblem(env), null);
  assert.equal(looksLikeDisplayFailure('Missing X server or $DISPLAY'), true);
});

test('ordinary Electron noise is not mistaken for a display failure', () => {
  const noise = [
    'Maximum number of clients reached',
    'DevTools listening on ws://127.0.0.1:9223/devtools/browser/1e2a',
    '[desktop] acp state: ready',
    'ERROR:ui/gl/angle_platform_impl.cc:42] ANGLE Display::initialize error 12289: Could not open the default X display.',
    'Request Autofill.enable failed. {"code":-32601,"message":"\'Autofill.enable\' wasn\'t found"}',
  ];
  for (const line of noise) {
    assert.equal(looksLikeDisplayFailure(line), false, `should not match: ${line}`);
  }
});
