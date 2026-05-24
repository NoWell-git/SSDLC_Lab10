// TEST
package main

import (
    "crypto/rand"
    "database/sql"
    "encoding/base64"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "os"
    "regexp"
    "strconv"
    "strings"
    "time"

    _ "github.com/lib/pq"
)


type TableInfo struct {
    Name    string
    Columns []string
}

var (
    db             *sql.DB
    tables         []TableInfo
    relatedTables  []string
    whiteListRegex = regexp.MustCompile(`^[a-zA-Zа-яА-ЯёЁ0-9\s\-\.]+$`)
)


var sessions = make(map[string]string)


func loadTableInfo() {
    tables = []TableInfo{
        {Name: "categories", Columns: []string{"id", "name", "description"}},
        {Name: "manufacturers", Columns: []string{"id", "name", "country", "founded_year"}},
        {Name: "components", Columns: []string{"id", "name", "category_id", "manufacturer_id", "model", "price"}},
        {Name: "stock", Columns: []string{"id", "component_id", "quantity", "warehouse_location"}},
    }
}

func connectToDatabase(username, password string) error {
    host := os.Getenv("DB_HOST")
    port := os.Getenv("DB_PORT")
    dbname := os.Getenv("DB_NAME")
    sslmode := os.Getenv("DB_SSLMODE")

    connStr := fmt.Sprintf("host=%s port=%s dbname=%s user=%s password=%s sslmode=%s",
        host, port, dbname, username, password, sslmode)

    var err error
    db, err = sql.Open("postgres", connStr)
    if err != nil {
        return fmt.Errorf("ошибка создания подключения")
    }
    if err := db.Ping(); err != nil {
        return fmt.Errorf("ошибка подключения к БД")
    }
    return nil
}


func generateToken() string {
    b := make([]byte, 32)
    rand.Read(b)
    return base64.URLEncoding.EncodeToString(b)
}

func authMiddleware(next http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        cookie, err := r.Cookie("session_token")
        if err != nil || sessions[cookie.Value] == "" {
            http.Redirect(w, r, "/login", http.StatusSeeOther)
            return
        }
        next(w, r)
    }
}

func renderHTML(w http.ResponseWriter, html string) {
    w.Header().Set("Content-Type", "text/html; charset=utf-8")
    fmt.Fprint(w, html)
}


func loginHandler(w http.ResponseWriter, r *http.Request) {
    if r.Method == http.MethodPost {
        user := r.FormValue("username")
        pass := r.FormValue("password")
        if err := connectToDatabase(user, pass); err != nil {
            log.Printf("[LOGIN FAIL] %s from %s at %s", user, r.RemoteAddr, time.Now().Format(time.RFC3339))
            renderHTML(w, `<!DOCTYPE html>
<html><head><title>Вход</title></head><body>
<h1>Вход в систему</h1>
<div style="color:red;">Неверный логин или пароль</div>
<form method="post">
Логин: <input name="username"><br>
Пароль: <input type="password" name="password"><br>
<input type="submit" value="Войти">
</form>
</body></html>`)
            return
        }
        loadTableInfo()
        relatedTables = []string{"components и stock", "categories и components", "manufacturers и components"}
        token := generateToken()
        sessions[token] = user
        http.SetCookie(w, &http.Cookie{
            Name:     "session_token",
            Value:    token,
            HttpOnly: true,
            Path:     "/",
        })
        http.Redirect(w, r, "/", http.StatusSeeOther)
        return
    }
    renderHTML(w, `<!DOCTYPE html>
<html><head><title>Вход</title></head><body>
<h1>Вход в систему</h1>
<form method="post">
Логин: <input name="username"><br>
Пароль: <input type="password" name="password"><br>
<input type="submit" value="Войти">
</form>
</body></html>`)
}

func logoutHandler(w http.ResponseWriter, r *http.Request) {
    cookie, err := r.Cookie("session_token")
    if err == nil {
        delete(sessions, cookie.Value)
    }
    http.SetCookie(w, &http.Cookie{Name: "session_token", MaxAge: -1})
    http.Redirect(w, r, "/login", http.StatusSeeOther)
}


func homeHandler(w http.ResponseWriter, r *http.Request) {
    renderHTML(w, `<!DOCTYPE html>
<html><head><title>Главная</title></head><body>
<h1>Управление базой данных PC Components</h1>
<ul>
<li><a href="/tables">Просмотр таблиц и фильтрация</a></li>
<li><a href="/insert">Добавить запись в одну таблицу</a></li>
<li><a href="/update">Массовое обновление записей</a></li>
<li><a href="/insert-related">Добавить запись в связанные таблицы</a></li>
</ul>
<a href="/logout">Выйти</a>
</body></html>`)
}


func columnsAPI(w http.ResponseWriter, r *http.Request) {
    tableName := r.URL.Query().Get("table")
    for _, t := range tables {
        if t.Name == tableName {
            w.Header().Set("Content-Type", "application/json")
            json.NewEncoder(w).Encode(t.Columns)
            return
        }
    }
    w.WriteHeader(http.StatusNotFound)
    json.NewEncoder(w).Encode([]string{})
}


func getTablesJSON() string {
    var sb strings.Builder
    sb.WriteString("[")
    for i, t := range tables {
        if i > 0 {
            sb.WriteString(",")
        }
        sb.WriteString(fmt.Sprintf(`{"name":"%s","columns":[`, t.Name))
        for j, c := range t.Columns {
            if j > 0 {
                sb.WriteString(",")
            }
            sb.WriteString(`"` + c + `"`)
        }
        sb.WriteString("]}")
    }
    sb.WriteString("]")
    return sb.String()
}


func tablesHandler(w http.ResponseWriter, r *http.Request) {
    if r.Method == http.MethodPost {
        tableName := r.FormValue("table")
        filterCount, _ := strconv.Atoi(r.FormValue("filter_count"))
        var conditions []string
        var args []interface{}
        for i := 0; i < filterCount; i++ {
            col := r.FormValue(fmt.Sprintf("col_%d", i))
            val := r.FormValue(fmt.Sprintf("val_%d", i))
            if col != "" && val != "" {
                if !whiteListRegex.MatchString(val) {
                    renderHTML(w, tablesFormHTML("Недопустимые символы в фильтре"))
                    return
                }
                conditions = append(conditions, fmt.Sprintf("%s = $%d", col, len(args)+1))
                args = append(args, val)
            }
        }
        query := fmt.Sprintf("SELECT * FROM %s", tableName)
        if len(conditions) > 0 {
            query += " WHERE " + strings.Join(conditions, " AND ")
        }
        query += " ORDER BY id"
        rows, err := db.Query(query, args...)
        if err != nil {
            renderHTML(w, tablesFormHTML("Ошибка выполнения запроса"))
            return
        }
        defer rows.Close()
        cols, _ := rows.Columns()
        var data [][]string
        for rows.Next() {
            vals := make([]interface{}, len(cols))
            ptrs := make([]interface{}, len(cols))
            for i := range vals {
                ptrs[i] = &vals[i]
            }
            rows.Scan(ptrs...)
            row := make([]string, len(cols))
            for i, v := range vals {
                if v == nil {
                    row[i] = "NULL"
                } else {
                    row[i] = fmt.Sprintf("%v", v)
                }
            }
            data = append(data, row)
        }
        renderHTML(w, tablesResultHTML(tableName, cols, data))
        return
    }
    renderHTML(w, tablesFormHTML(""))
}


func insertHandler(w http.ResponseWriter, r *http.Request) {
    if r.Method == http.MethodPost {
        tableName := r.FormValue("table")
        var table TableInfo
        for _, t := range tables {
            if t.Name == tableName {
                table = t
                break
            }
        }
        if table.Name == "" {
            renderHTML(w, insertFormHTML("Таблица не найдена"))
            return
        }
        insertCols := table.Columns[1:]
        values := make([]interface{}, len(insertCols))
        for i, col := range insertCols {
            val := r.FormValue(col)
            if !whiteListRegex.MatchString(val) {
                renderHTML(w, insertFormHTML("Недопустимые символы в поле "+col))
                return
            }
            if col == "price" || col == "quantity" || col == "founded_year" ||
                col == "category_id" || col == "manufacturer_id" || col == "component_id" {
                if _, err := strconv.Atoi(val); err != nil {
                    renderHTML(w, insertFormHTML("Поле "+col+" должно быть числом"))
                    return
                }
            }
            values[i] = val
        }
        placeholders := make([]string, len(insertCols))
        for i := range placeholders {
            placeholders[i] = fmt.Sprintf("$%d", i+1)
        }
        query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", tableName, strings.Join(insertCols, ", "), strings.Join(placeholders, ", "))
        _, err := db.Exec(query, values...)
        if err != nil {
            renderHTML(w, insertFormHTML("Ошибка добавления: "+err.Error()))
            return
        }
        renderHTML(w, insertSuccessHTML())
        return
    }
    renderHTML(w, insertFormHTML(""))
}


func updateHandler(w http.ResponseWriter, r *http.Request) {
    if r.Method == http.MethodPost {
        tableName := r.FormValue("table")
        column := r.FormValue("column")
        idsText := r.FormValue("ids")
        newValue := r.FormValue("new_value")

        idStrs := strings.FieldsFunc(idsText, func(c rune) bool {
            return c == ',' || c == '\n' || c == ' '
        })
        if len(idStrs) == 0 {
            renderHTML(w, updateFormHTML("Введите хотя бы один ID"))
            return
        }
        ids := make([]int, 0, len(idStrs))
        for _, s := range idStrs {
            id, err := strconv.Atoi(strings.TrimSpace(s))
            if err != nil {
                renderHTML(w, updateFormHTML("ID должен быть числом"))
                return
            }
            ids = append(ids, id)
        }
        if !whiteListRegex.MatchString(newValue) {
            renderHTML(w, updateFormHTML("Недопустимые символы в новом значении"))
            return
        }
        placeholders := make([]string, len(ids))
        args := []interface{}{newValue}
        for i, id := range ids {
            placeholders[i] = fmt.Sprintf("$%d", i+2)
            args = append(args, id)
        }
        query := fmt.Sprintf("UPDATE %s SET %s = $1 WHERE id IN (%s)", tableName, column, strings.Join(placeholders, ", "))
        result, err := db.Exec(query, args...)
        if err != nil {
            renderHTML(w, updateFormHTML("Ошибка обновления: "+err.Error()))
            return
        }
        rows, _ := result.RowsAffected()
        renderHTML(w, updateSuccessHTML(rows))
        return
    }
    renderHTML(w, updateFormHTML(""))
}


func insertRelatedHandler(w http.ResponseWriter, r *http.Request) {
    if r.Method == http.MethodPost {
        
        relationIndex, err := strconv.Atoi(r.FormValue("relation"))
        if err != nil || relationIndex < 1 || relationIndex > len(relatedTables) {
            renderHTML(w, insertRelatedFormHTML("Неверный выбор связанных таблиц"))
            return
        }
        recordCount, err := strconv.Atoi(r.FormValue("record_count"))
        if err != nil || recordCount < 1 {
            renderHTML(w, insertRelatedFormHTML("Количество записей должно быть больше 0"))
            return
        }

        relation := relatedTables[relationIndex-1]
        tablesInRelation := strings.Split(relation, " и ")
        if len(tablesInRelation) != 2 {
            renderHTML(w, insertRelatedFormHTML("Ошибка формата связанных таблиц"))
            return
        }

        var table1, table2 TableInfo
        for _, t := range tables {
            if t.Name == tablesInRelation[0] {
                table1 = t
            }
            if t.Name == tablesInRelation[1] {
                table2 = t
            }
        }


        for i := 0; i < recordCount; i++ {

            insertCols1 := table1.Columns[1:]
            values1 := make([]interface{}, len(insertCols1))
            for j, col := range insertCols1 {
                val := r.FormValue(fmt.Sprintf("t1_%d_%s", i, col))
                if !whiteListRegex.MatchString(val) {
                    renderHTML(w, insertRelatedFormHTML("Недопустимые символы в поле "+col))
                    return
                }
                if col == "price" || col == "founded_year" || col == "category_id" || col == "manufacturer_id" {
                    if _, err := strconv.Atoi(val); err != nil {
                        renderHTML(w, insertRelatedFormHTML("Поле "+col+" должно быть числом"))
                        return
                    }
                }
                values1[j] = val
            }
            placeholders1 := make([]string, len(insertCols1))
            for j := range placeholders1 {
                placeholders1[j] = fmt.Sprintf("$%d", j+1)
            }
            query1 := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) RETURNING id",
                table1.Name, strings.Join(insertCols1, ", "), strings.Join(placeholders1, ", "))
            var insertedID int
            err := db.QueryRow(query1, values1...).Scan(&insertedID)
            if err != nil {
                renderHTML(w, insertRelatedFormHTML("Ошибка вставки в первую таблицу: "+err.Error()))
                return
            }

 
            var foreignKeyColumn string
            for _, col := range table2.Columns {
                if col == "component_id" || col == "category_id" || col == "manufacturer_id" {
                    if strings.Contains(table2.Name, "stock") && table1.Name == "components" && col == "component_id" {
                        foreignKeyColumn = col
                        break
                    } else if strings.Contains(table2.Name, "components") {
                        if table1.Name == "categories" && col == "category_id" {
                            foreignKeyColumn = col
                            break
                        } else if table1.Name == "manufacturers" && col == "manufacturer_id" {
                            foreignKeyColumn = col
                            break
                        }
                    }
                }
            }
            if foreignKeyColumn == "" {
                for _, col := range table2.Columns {
                    if col != "id" {
                        foreignKeyColumn = col
                        break
                    }
                }
            }


            insertCols2 := table2.Columns[1:]
            values2 := make([]interface{}, len(insertCols2))
            for j, col := range insertCols2 {
                if col == foreignKeyColumn {
                    values2[j] = insertedID
                    continue
                }
                val := r.FormValue(fmt.Sprintf("t2_%d_%s", i, col))
                if !whiteListRegex.MatchString(val) {
                    renderHTML(w, insertRelatedFormHTML("Недопустимые символы в поле "+col))
                    return
                }
                if col == "quantity" || col == "price" {
                    if _, err := strconv.Atoi(val); err != nil {
                        renderHTML(w, insertRelatedFormHTML("Поле "+col+" должно быть числом"))
                        return
                    }
                }
                values2[j] = val
            }
            placeholders2 := make([]string, len(insertCols2))
            for j := range placeholders2 {
                placeholders2[j] = fmt.Sprintf("$%d", j+1)
            }
            query2 := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
                table2.Name, strings.Join(insertCols2, ", "), strings.Join(placeholders2, ", "))
            _, err = db.Exec(query2, values2...)
            if err != nil {
                renderHTML(w, insertRelatedFormHTML("Ошибка вставки во вторую таблицу: "+err.Error()))
                return
            }
        }
        renderHTML(w, insertRelatedSuccessHTML(recordCount))
        return
    }
    renderHTML(w, insertRelatedFormHTML(""))
}


func tablesFormHTML(errMsg string) string {
    options := ""
    for _, t := range tables {
        options += fmt.Sprintf("<option value='%s'>%s</option>", t.Name, t.Name)
    }
    tablesJSON := getTablesJSON()
    errDiv := ""
    if errMsg != "" {
        errDiv = fmt.Sprintf("<div style='color:red;'>%s</div>", errMsg)
    }
    return fmt.Sprintf(`<!DOCTYPE html>
<html><head><title>Просмотр таблиц</title><style>table,th,td{border:1px solid #ccc;border-collapse:collapse;padding:6px;}</style></head><body>
<h1>Просмотр таблиц</h1>
%s
<form method="post" id="filterForm">
<label>Таблица:</label> <select name="table" id="tableSelect">%s</select><br>
<div id="filters"></div>
<button type="button" onclick="addFilter()">Добавить фильтр</button>
<input type="hidden" name="filter_count" id="filterCount">
<br><input type="submit" value="Показать">
</form>
<script>
let filterIdx=0;
const tablesData = %s;
function addFilter(){
    const div=document.createElement('div');
    const idx=filterIdx++;
    div.innerHTML='<select name="col_'+idx+'"></select> <input name="val_'+idx+'" placeholder="значение"> <button type="button" onclick="this.parentElement.remove()">x</button>';
    document.getElementById('filters').appendChild(div);
    updateColumns();
}
function updateColumns(){
    const table=document.getElementById('tableSelect').value;
    fetch('/columns?table='+encodeURIComponent(table))
        .then(res=>res.json())
        .then(cols=>{
            document.querySelectorAll('[name^="col_"]').forEach(select=>{
                const cur=select.value;
                select.innerHTML=cols.map(c=>'<option value="'+c+'">'+c+'</option>').join('');
                if(cur) select.value=cur;
            });
        });
}
document.getElementById('tableSelect').onchange=updateColumns;
document.getElementById('filterForm').onsubmit=function(){document.getElementById('filterCount').value=filterIdx;};
addFilter();
</script>
<a href="/">Главная</a> | <a href="/logout">Выйти</a>
</body></html>`, errDiv, options, tablesJSON)
}

func tablesResultHTML(tableName string, cols []string, rows [][]string) string {
    var sb strings.Builder
    sb.WriteString(fmt.Sprintf("<h2>Таблица: %s</h2>", tableName))
    if len(rows) == 0 {
        sb.WriteString("<p>Нет записей</p>")
    } else {
        sb.WriteString("<table><thead><tr>")
        for _, c := range cols {
            sb.WriteString("<th>" + c + "</th>")
        }
        sb.WriteString("</tr></thead><tbody>")
        for _, row := range rows {
            sb.WriteString("<tr>")
            for _, cell := range row {
                sb.WriteString("<td>" + cell + "</td>")
            }
            sb.WriteString("</tr>")
        }
        sb.WriteString("</tbody></table>")
        sb.WriteString(fmt.Sprintf("<p>Всего записей: %d</p>", len(rows)))
    }
    sb.WriteString(`<br><a href="/tables">Назад</a> | <a href="/">Главная</a> | <a href="/logout">Выйти</a>`)
    return "<!DOCTYPE html><html><head><title>Результат</title><style>table,th,td{border:1px solid #ccc;border-collapse:collapse;padding:6px;}</style></head><body>" + sb.String() + "</body></html>"
}

func insertFormHTML(errMsg string) string {
    options := ""
    for _, t := range tables {
        options += fmt.Sprintf("<option value='%s'>%s</option>", t.Name, t.Name)
    }
    tablesJSON := getTablesJSON()
    errDiv := ""
    if errMsg != "" {
        errDiv = fmt.Sprintf("<div style='color:red;'>%s</div>", errMsg)
    }
    return fmt.Sprintf(`<!DOCTYPE html>
<html><head><title>Добавление записи</title></head><body>
<h1>Добавление записи</h1>
%s
<form method="post" id="insertForm">
<label>Таблица:</label> <select name="table" id="tableSelect">%s</select><br>
<div id="fields"></div>
<input type="submit" value="Добавить">
</form>
<script>
const tablesData = %s;
function loadFields(){
    const table=document.getElementById('tableSelect').value;
    const cols=tablesData.find(t=>t.name===table).columns.slice(1);
    document.getElementById('fields').innerHTML=cols.map(c=>'<label>'+c+':</label> <input name="'+c+'" required><br>').join('');
}
document.getElementById('tableSelect').onchange=loadFields;
loadFields();
</script>
<a href="/">Главная</a> | <a href="/logout">Выйти</a>
</body></html>`, errDiv, options, tablesJSON)
}

func insertSuccessHTML() string {
    return `<!DOCTYPE html>
<html><head><title>Успех</title></head><body>
<h1>Запись добавлена</h1>
<p>Запись успешно добавлена.</p>
<a href="/insert">Добавить ещё</a> | <a href="/">Главная</a> | <a href="/logout">Выйти</a>
</body></html>`
}

func updateFormHTML(errMsg string) string {
    options := ""
    for _, t := range tables {
        options += fmt.Sprintf("<option value='%s'>%s</option>", t.Name, t.Name)
    }
    tablesJSON := getTablesJSON()
    errDiv := ""
    if errMsg != "" {
        errDiv = fmt.Sprintf("<div style='color:red;'>%s</div>", errMsg)
    }
    return fmt.Sprintf(`<!DOCTYPE html>
<html><head><title>Обновление записей</title></head><body>
<h1>Обновление записей</h1>
%s
<form method="post">
<label>Таблица:</label> <select name="table" id="tableSelect">%s</select><br>
<label>Колонка для обновления:</label> <select name="column" id="columnSelect"></select><br>
<label>ID записей (через запятую, пробел или с новой строки):</label><br>
<textarea name="ids" rows="4" cols="40" required></textarea><br>
<label>Новое значение:</label> <input name="new_value" required><br>
<input type="submit" value="Обновить">
</form>
<script>
const tablesData = %s;
function loadColumns(){
    const table=document.getElementById('tableSelect').value;
    const cols=tablesData.find(t=>t.name===table).columns.filter(c=>c!=='id');
    const sel=document.getElementById('columnSelect');
    sel.innerHTML=cols.map(c=>'<option value="'+c+'">'+c+'</option>').join('');
}
document.getElementById('tableSelect').onchange=loadColumns;
loadColumns();
</script>
<a href="/">Главная</a> | <a href="/logout">Выйти</a>
</body></html>`, errDiv, options, tablesJSON)
}

func updateSuccessHTML(rowsAffected int64) string {
    return fmt.Sprintf(`<!DOCTYPE html>
<html><head><title>Успех</title></head><body>
<h1>Обновление выполнено</h1>
<p>Обновлено записей: %d</p>
<a href="/update">Обновить ещё</a> | <a href="/">Главная</a> | <a href="/logout">Выйти</a>
</body></html>`, rowsAffected)
}

func insertRelatedFormHTML(errMsg string) string {

    relationOptions := ""
    for i, rel := range relatedTables {
        relationOptions += fmt.Sprintf("<option value='%d'>%s</option>", i+1, rel)
    }
    errDiv := ""
    if errMsg != "" {
        errDiv = fmt.Sprintf("<div style='color:red;'>%s</div>", errMsg)
    }
    return fmt.Sprintf(`<!DOCTYPE html>
<html><head><title>Добавление в связанные таблицы</title><script>
let currentRecordCount = 1;
function updateForms() {
    const relation = document.getElementById('relation').value;
    const count = document.getElementById('record_count').value;
    if(count < 1) { alert('Количество записей должно быть >=1'); return; }
    currentRecordCount = parseInt(count);
    const container = document.getElementById('dynamic-forms');
    container.innerHTML = '';
    
    fetch('/relation-info?idx=' + relation)
        .then(res => res.json())
        .then(data => {
            for(let r=0; r<currentRecordCount; r++) {
                let formHtml = '<div style="border:1px solid #ccc; margin:10px; padding:10px;"><h3>Запись '+(r+1)+'</h3>';
                formHtml += '<h4>Таблица 1: '+data.table1+'</h4>';
                data.cols1.forEach(col => {
                    formHtml += '<label>'+col+':</label> <input name="t1_'+r+'_'+col+'" required><br>';
                });
                formHtml += '<h4>Таблица 2: '+data.table2+' (внешний ключ будет подставлен автоматически)</h4>';
                data.cols2.forEach(col => {
                    if(col === data.fk) {
                        formHtml += '<input type="hidden" name="t2_'+r+'_'+col+'" value="auto">';
                    } else {
                        formHtml += '<label>'+col+':</label> <input name="t2_'+r+'_'+col+'" required><br>';
                    }
                });
                formHtml += '</div>';
                container.innerHTML += formHtml;
            }
        });
}
window.onload = function() {
    updateForms();
    document.getElementById('relation').onchange = updateForms;
    document.getElementById('record_count').onchange = updateForms;
};
</script></head><body>
<h1>Добавление записей в связанные таблицы</h1>
%s
<form method="post">
<label>Выберите связанные таблицы:</label> <select name="relation" id="relation">%s</select><br>
<label>Количество записей:</label> <input type="number" name="record_count" id="record_count" value="1" min="1"><br>
<div id="dynamic-forms"></div>
<input type="submit" value="Добавить">
</form>
<a href="/">Главная</a> | <a href="/logout">Выйти</a>
</body></html>`, errDiv, relationOptions)
}


func relationInfoHandler(w http.ResponseWriter, r *http.Request) {
    idxStr := r.URL.Query().Get("idx")
    idx, err := strconv.Atoi(idxStr)
    if err != nil || idx < 1 || idx > len(relatedTables) {
        w.WriteHeader(http.StatusBadRequest)
        return
    }
    relation := relatedTables[idx-1]
    parts := strings.Split(relation, " и ")
    if len(parts) != 2 {
        w.WriteHeader(http.StatusInternalServerError)
        return
    }
    var table1, table2 TableInfo
    for _, t := range tables {
        if t.Name == parts[0] {
            table1 = t
        }
        if t.Name == parts[1] {
            table2 = t
        }
    }

    fk := ""
    for _, col := range table2.Columns {
        if col == "component_id" || col == "category_id" || col == "manufacturer_id" {
            if strings.Contains(table2.Name, "stock") && table1.Name == "components" && col == "component_id" {
                fk = col
                break
            } else if strings.Contains(table2.Name, "components") {
                if table1.Name == "categories" && col == "category_id" {
                    fk = col
                    break
                } else if table1.Name == "manufacturers" && col == "manufacturer_id" {
                    fk = col
                    break
                }
            }
        }
    }
    if fk == "" {
        for _, col := range table2.Columns {
            if col != "id" {
                fk = col
                break
            }
        }
    }
    resp := map[string]interface{}{
        "table1": table1.Name,
        "table2": table2.Name,
        "cols1":  table1.Columns[1:],
        "cols2":  table2.Columns[1:],
        "fk":     fk,
    }
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(resp)
}

func insertRelatedSuccessHTML(recordCount int) string {
    return fmt.Sprintf(`<!DOCTYPE html>
<html><head><title>Успех</title></head><body>
<h1>Связанные записи добавлены</h1>
<p>Добавлено %d связанных записей.</p>
<a href="/insert-related">Добавить ещё</a> | <a href="/">Главная</a> | <a href="/logout">Выйти</a>
</body></html>`, recordCount)
}


func startWebServer() {
    loadTableInfo()
    relatedTables = []string{"components и stock", "categories и components", "manufacturers и components"}

    http.HandleFunc("/login", loginHandler)
    http.HandleFunc("/logout", logoutHandler)
    http.HandleFunc("/columns", authMiddleware(columnsAPI))
    http.HandleFunc("/relation-info", authMiddleware(relationInfoHandler))
    http.HandleFunc("/", authMiddleware(homeHandler))
    http.HandleFunc("/tables", authMiddleware(tablesHandler))
    http.HandleFunc("/insert", authMiddleware(insertHandler))
    http.HandleFunc("/update", authMiddleware(updateHandler))
    http.HandleFunc("/insert-related", authMiddleware(insertRelatedHandler))

    port := os.Getenv("WEB_PORT")
    if port == "" {
        port = "8081"
    }
    log.Printf("Веб-сервер запущен на порту %s", port)
    log.Fatal(http.ListenAndServe(":"+port, nil))
}

func main() {
    startWebServer()
}
