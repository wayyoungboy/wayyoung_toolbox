import {describe,expect,it,vi} from 'vitest';
import {createCompatiblePlayback, absolutePlaybackPosition, shiftSubtitleText} from './compatible-playback';

describe('compatible playback lifecycle',()=>{
 it('reaps a session whose creation completes after close',async()=>{
  let resolve;
  const create=vi.fn(()=>new Promise(done=>{resolve=done;}));
  const stop=vi.fn().mockResolvedValue({});const ready=vi.fn();
  const player=createCompatiblePlayback({create,stop,onSession:ready});
  const pending=player.start({start:12});await vi.waitFor(()=>expect(create).toHaveBeenCalledOnce());
  const closing=player.close();resolve({id:'late'});await pending;await closing;
  expect(stop).toHaveBeenCalledExactlyOnceWith('late');expect(ready).not.toHaveBeenCalled();
 });
 it('serializes seek sessions and discards obsolete pending requests',async()=>{
  let resolve;const first=new Promise(done=>{resolve=done;});
  const create=vi.fn().mockReturnValueOnce(first).mockResolvedValueOnce({id:'latest'});
  const stop=vi.fn().mockResolvedValue({});const ready=vi.fn();
  const player=createCompatiblePlayback({create,stop,onSession:ready});
  const a=player.start({start:0});await vi.waitFor(()=>expect(create).toHaveBeenCalledOnce());
  const b=player.start({start:20}),c=player.start({start:40});
  resolve({id:'obsolete'});await Promise.all([a,b,c]);
  expect(create.mock.calls.map(([request])=>request.start)).toEqual([0,40]);
  expect(stop).toHaveBeenCalledWith('obsolete');expect(ready).toHaveBeenCalledExactlyOnceWith({id:'latest'});
  await player.close();expect(stop).toHaveBeenLastCalledWith('latest');
 });
 it('keeps one absolute movie timeline and shifts local VTT timestamps after seeking',()=>{
  expect(absolutePlaybackPosition({offset:40,duration:100},5)).toBe(45);
  expect(absolutePlaybackPosition({offset:40,duration:100},Infinity)).toBe(40);
  expect(shiftSubtitleText('WEBVTT\n\n00:00:39.000 --> 00:00:42.000\nA\n\n00:00:45.000 --> 00:00:48.000\nB\n',40)).toContain('00:00:00.000 --> 00:00:02.000');
  expect(shiftSubtitleText('WEBVTT\n\n00:00:45.000 --> 00:00:48.000\nB\n',40)).toContain('00:00:05.000 --> 00:00:08.000');
 });
});
