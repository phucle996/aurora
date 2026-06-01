export default function KeyValueTable({ headers, rows }) {
  return (
    <div className="overflow-x-auto rounded-lg border border-slate-200 dark:border-slate-800 my-4">
      <table className="w-full text-base">
        <thead>
          <tr className="bg-gradient-to-r from-indigo-100 to-purple-100 dark:from-indigo-600/30 dark:to-purple-600/30">
            {headers.map((h) => (
              <th
                key={h}
                className="px-4 py-2.5 text-left text-sm uppercase tracking-wider font-semibold text-indigo-700 dark:text-indigo-100 border-b border-slate-200 dark:border-slate-700"
              >
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="bg-white dark:bg-transparent">
          {rows.map((row, i) => (
            <tr
              key={i}
              className="border-b border-slate-200 dark:border-slate-800 last:border-b-0 hover:bg-slate-50 dark:hover:bg-slate-800/40"
            >
              {row.map((cell, j) => (
                <td key={j} className="px-4 py-2.5 text-slate-700 dark:text-slate-300">
                  {cell}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
