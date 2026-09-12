export default function WorkspaceTabs({ label, items, selected, onChange }) {
  const onKeyDown = (event, index) => {
    let next;
    if (event.key === 'ArrowRight') next = (index + 1) % items.length;
    else if (event.key === 'ArrowLeft') next = (index + items.length - 1) % items.length;
    else if (event.key === 'Home') next = 0;
    else if (event.key === 'End') next = items.length - 1;
    else return;
    event.preventDefault();
    onChange(items[next].key);
    event.currentTarget.parentElement.children[next]?.focus();
  };
  return <div className="workspace-tabs" role="tablist" aria-label={label}>{items.map((item, index) => <button type="button" role="tab" aria-selected={selected === item.key} tabIndex={selected === item.key ? 0 : -1} className={selected === item.key ? 'selected' : ''} onClick={() => onChange(item.key)} onKeyDown={(event) => onKeyDown(event, index)} key={item.key}>{item.label}</button>)}</div>;
}
