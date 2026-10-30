// RemoteIcon renders the brand mark for a project's remote host, when
// known. Icons live in /icons/*.svg (Simple Icons and the official
// Azure glyph). GitHub's mark is near-black, so on a dark background
// the caller inverts it via the .remote-tint rule.
export default function RemoteIcon({ remote, size = 12 }: { remote?: string; size?: number }) {
  // 'git' is the fallback mark for a repository whose origin is not a
  // known host, or absent; undefined hides the icon entirely.
  const src = remote === 'github' || remote === 'gitlab' || remote === 'azure' ? `/icons/${remote}.svg` : `/icons/git.svg`;
  const label = remote === 'azure' ? 'Azure DevOps' : remote ? remote[0].toUpperCase() + remote.slice(1) : 'Git';
  return (
    <img
      src={src}
      alt={label}
      title={label}
      width={size}
      height={size}
      className={remote === 'github' ? 'remote-tint' : undefined}
      style={{ display: 'block' }}
    />
  );
}
