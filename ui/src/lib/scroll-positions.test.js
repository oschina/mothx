import assert from 'node:assert/strict';
import {
	clearScrollPositions,
	forgetScrollPosition,
	getScrollPosition,
	saveScrollPosition,
	scrollPositionCount
} from './scroll-positions.js';

clearScrollPositions();
assert.equal(getScrollPosition(''), null);
assert.equal(getScrollPosition('missing'), null);

saveScrollPosition('a', { top: 120, following: false });
assert.deepEqual(getScrollPosition('a'), { top: 120, following: false });

saveScrollPosition('a', { top: 40, following: true });
assert.deepEqual(getScrollPosition('a'), { top: 40, following: true }, 'later position wins');

saveScrollPosition('b', { top: Number.NaN, following: false });
saveScrollPosition('c', { top: -1, following: false });
assert.equal(getScrollPosition('b'), null, 'invalid offsets are ignored');
assert.equal(getScrollPosition('c'), null, 'negative offsets are ignored');

saveScrollPosition('b', { top: 0, following: true });
assert.deepEqual(getScrollPosition('b'), { top: 0, following: true });

forgetScrollPosition('a');
assert.equal(getScrollPosition('a'), null);
forgetScrollPosition('');
forgetScrollPosition('missing');

clearScrollPositions();
for (let i = 0; i < 80; i += 1) saveScrollPosition(`session-${i}`, { top: i, following: false });
assert.equal(scrollPositionCount(), 64, 'positions are bounded');
assert.equal(getScrollPosition('session-0'), null, 'oldest position is evicted');
assert.equal(getScrollPosition('session-79')?.top, 79);

clearScrollPositions();
console.log('scroll positions ok');
